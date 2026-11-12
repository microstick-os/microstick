/* Native session bridge for hash-pinned linux-msm/qdl. Initial I/R access is
 * read-only. A validated exact-range grant enables planned writes and mandatory
 * before/input/readback hashes. No reset, erase, provisioning, RPMB or EXT_CSD.
 */
#define _FILE_OFFSET_BITS 64
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <errno.h>
#include <inttypes.h>
#include <libusb-1.0/libusb.h>
#include "qdl.h"
#include "programmer.h"
#include "edl-plan.h"
#include "edl-session.h"
#include "edl-security.h"
#include "edl-usb.h"

bool qdl_debug;
static uint32_t chip_serial;
static uint64_t chip_hwid;
static const char *session_state="sahara";
void microstick_identity(uint32_t serial, uint64_t hwid) { chip_serial=serial; chip_hwid=hwid; }
/* Emit only fixed stage codes. Upstream output can contain hardware identity
 * and is deliberately excluded from user-visible errors and ordinary logs. */
static int stage_error(FILE *wire,const char *stage) {
    fprintf(wire,"E %s\n",stage);fflush(wire);return 3;
}
static int info(struct qdl_device *q, FILE *wire, size_t sizes[3]) {
    size_t ss;
    if(!sizes[0])for (int lun=0;lun<3;lun++) {
        if (firehose_getsize(q,lun,&ss,&sizes[lun]) || ss!=512 || sizes[lun]==0 || sizes[lun]>SIZE_MAX/512) {
            stage_error(wire,lun==0?"geometry-user":lun==1?"geometry-boot1":"geometry-boot2");return -1;
        }
    }
    fprintf(wire,"I2 %s %08" PRIx32 " %016" PRIx64 " %zu %zu %zu\n",session_state,chip_serial,chip_hwid,sizes[0]*512,sizes[1]*512,sizes[2]*512);
    return fflush(wire);
}
/* QDL omits nodes whose libusb_open failed. Preserve ACCESS as its own stage
 * instead of turning it into device-not-found. This only opens/closes USB; it
 * never claims an interface, starts Sahara or changes ACL/udev. */
static int usb_access_probe(unsigned *nodes,const char *selector,bool list) {
    libusb_context *ctx=NULL;libusb_device **devices=NULL;
    if(libusb_init(&ctx))return -1;
    ssize_t n=libusb_get_device_list(ctx,&devices);int denied=0;*nodes=0;
    if(n<0){libusb_exit(ctx);return -1;}
    for(ssize_t i=0;i<n;i++){
        struct libusb_device_descriptor desc;
        if(libusb_get_device_descriptor(devices[i],&desc)||desc.idVendor!=0x05c6||desc.idProduct!=0x9008)continue;
        char path[64];if(microstick_usb_path(devices[i],path,sizeof(path)))continue;
        if(selector&&selector[0]=='@'&&strcmp(path,selector))continue;
        (*nodes)++;
        if(list){puts(path);continue;}
        libusb_device_handle *handle=NULL;int ret=libusb_open(devices[i],&handle);
        if(ret==LIBUSB_ERROR_ACCESS)denied++;
        if(!ret)libusb_close(handle);
    }
    libusb_free_device_list(devices,1);libusb_exit(ctx);
    return denied;
}
int main(int argc,char **argv) {
    if(argc==2&&!strcmp(argv[1],"--list")){unsigned nodes=0;return usb_access_probe(&nodes,NULL,true)<0?3:0;}
    if(argc!=3) return 2;
    FILE *wire=fdopen(dup(STDOUT_FILENO),"w");if(!wire)return 2;
    fflush(stdout);if(dup2(STDERR_FILENO,STDOUT_FILENO)<0)return 2;
    unsigned nodes=0;int access=usb_access_probe(&nodes,argv[2],false);
    if(access<0)return stage_error(wire,"usb-init");
    if(nodes>1&&!argv[2][0])return stage_error(wire,"multiple-devices");
    unsigned count=0, matched=0;struct qdl_device_desc *list=usb_list(&count);
    for(unsigned i=0;i<count;i++) if(list[i].vid==0x05c6 && list[i].pid==0x9008 && (!argv[2][0] || !strcmp(list[i].serial,argv[2]))) matched++;
    free(list);if(argv[2][0]=='@')matched=nodes-(unsigned)access;
    if(matched!=1)return stage_error(wire,matched==0?(access>0?"USB_PERMISSION_DENIED":"device-not-found"):"multiple-devices");
    struct qdl_device *q=qdl_init(QDL_DEVICE_USB);if(!q)return stage_error(wire,"usb-init");
    const char *failure="usb-open";
    if(qdl_open(q,argv[2][0]?argv[2]:NULL)) {
        stage_error(wire,failure);qdl_deinit(q);fclose(wire);return 3;
    }
    /* The reviewed source hook captures actual Sahara identity in memory. It is
     * never substituted by USB iSerial, an external metadata file or a GUID. */
    failure="session-detect";
    int state=microstick_session(q);if(state<0)goto fail;
    if(state==1){session_state="firehose";goto configured;}
    failure="sahara-identity";
    if(sahara_chipinfo(q) || !chip_serial || !chip_hwid)goto fail;
    struct sahara_image images[MAPPING_SZ]={0};
    failure="programmer-file";
    char *programmer=strdup(argv[1]);if(!programmer)goto fail;
    int ret=decode_programmer(programmer,images);free(programmer);if(ret<0)goto fail;
    failure="programmer-upload";
    ret=sahara_run(q,images,NULL,NULL);sahara_images_free(images,MAPPING_SZ);if(ret<0)goto fail;
configured:
    q->max_payload_size=16384;
    failure="firehose-configure";
    if(firehose_open(q,QDL_STORAGE_EMMC))goto fail;
    if(q->max_payload_size>16384)q->max_payload_size=16384;
    failure=NULL;
    size_t sizes[3]={0};if(info(q,wire,sizes))goto fail;
    char line[256];static unsigned char data[1048576];
    while(fgets(line,sizeof(line),stdin)) {
        if(!strcmp(line,"S\n")){
            /* The bridge/programmer is pinned to MSM8916. A known Sahara
             * identity must agree; warm Firehose still uses this same loader.
             */
            uint32_t msm=(uint32_t)(chip_hwid>>32)&0xffffff;
            if(msm && msm!=0x7050e1 && msm!=0x7060e1){stage_error(wire,"boot-security-soc");continue;}
            int secure=microstick_secure_boot(q);
            if(secure<0)stage_error(wire,"boot-security");
            else {fprintf(wire,"S %s\n",secure?"enabled":"disabled");fflush(wire);}
            continue;
        }
        if(line[0]=='A'){if(grant_plan(line,wire,sizes))goto fail;continue;}
        if(line[0]=='W'){if(write_planned_range(q,line,wire))goto fail;continue;}
        if(!strcmp(line,"I\n")){if(info(q,wire,sizes))goto fail;continue;}
        unsigned lun;unsigned long long off,len;char extra;
        if(sscanf(line,"R %u %llu %llu %c",&lun,&off,&len,&extra)!=3 || lun>2 || !len || len>sizeof(data) || off%512 || len%512 || off>sizes[lun]*512 || len>sizes[lun]*512-off)goto fail;
        if(firehose_pread(q,(int)lun,(size_t)(off/512),data,512,(size_t)(len/512))!=(ssize_t)len)goto fail;
        if(fwrite(data,1,(size_t)len,wire)!=len || fflush(wire))goto fail;
    }
    free(allowed);qdl_close(q);qdl_deinit(q);fclose(wire);return 0;
fail:
    if(failure)stage_error(wire,failure);
    free(allowed);qdl_close(q);qdl_deinit(q);fclose(wire);return 3;
}
