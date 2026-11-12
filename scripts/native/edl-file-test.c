/* Disposable regular-file Firehose stand-in. No USB code is linked here.
 * The exact authorization/hash/readback implementation is shared with EDL. */
#define _FILE_OFFSET_BITS 64
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <unistd.h>
#include <fcntl.h>
#include <sys/stat.h>
#include <inttypes.h>
struct qdl_device { int fd[3]; bool wrote; };
static const char *fault;
static ssize_t firehose_pread(struct qdl_device *q,int lun,size_t off,void *buf,size_t ss,size_t n) {
    if(lun<0||lun>2)return -1;
    ssize_t ret;
    if(q->fd[lun]<0){memset(buf,0,ss*n);ret=(ssize_t)(ss*n);}
    else ret=pread(q->fd[lun],buf,ss*n,off*ss);
    if(ret>0&&q->wrote&&fault&&!strcmp(fault,"readback"))((unsigned char *)buf)[0]^=1;
    return ret;
}
int microstick_next_write(void *,size_t);
static ssize_t firehose_pwrite(struct qdl_device *q,int lun,size_t off,const void *ignored,size_t ss,size_t n) {
    (void)ignored;if(lun<0||lun>2||q->fd[lun]<0)return -1;
    size_t total=ss*n;unsigned char buf[16384];size_t sent=0;
    while(sent<total){size_t count=total-sent;if(count>sizeof(buf))count=sizeof(buf);
        if(microstick_next_write(buf,count))return -1;
        if(pwrite(q->fd[lun],buf,count,off*ss+sent)!=(ssize_t)count)return -1;
        sent+=count;
        if(fault&&!strcmp(fault,"interrupt"))return -1;
    }
    if(fsync(q->fd[lun]))return -1;
    q->wrote=true;return (ssize_t)total;
}
#include "edl-plan.h"
static int device_info(struct qdl_device *q,size_t sizes[3]) {
    for(int i=0;i<3;i++) {struct stat st;if(q->fd[i]<0){sizes[i]=8;continue;}
        if(fstat(q->fd[i],&st)||!S_ISREG(st.st_mode)||st.st_size<=0||st.st_size%512)return -1;
        sizes[i]=(size_t)st.st_size/512;
    }
    if(getenv("MICROSTICK_NATIVE_TEST_WARM"))printf("I2 firehose 00000000 0000000000000000 %zu %zu %zu\n",sizes[0]*512,sizes[1]*512,sizes[2]*512);
    else {printf("I 00000001 0000000000000002 %zu %zu %zu\n",sizes[0]*512,sizes[1]*512,sizes[2]*512);}
    return fflush(stdout);
}
int main(void) {
    const char *vars[3]={"MICROSTICK_NATIVE_TEST_IMAGE","MICROSTICK_NATIVE_TEST_BOOT1","MICROSTICK_NATIVE_TEST_BOOT2"};
    struct qdl_device q={{-1,-1,-1},false};
    for(int i=0;i<3;i++){const char *path=getenv(vars[i]);if(!path){if(!i)return 3;continue;}
        q.fd[i]=open(path,O_RDWR|O_NOFOLLOW);if(q.fd[i]<0)return 3;}
    fault=getenv("MICROSTICK_NATIVE_TEST_FAULT");size_t sizes[3];if(device_info(&q,sizes))return 3;
    char line[256];while(fgets(line,sizeof(line),stdin)) {
        if(!strcmp(line,"I\n")){if(device_info(&q,sizes))return 3;continue;}
        if(line[0]=='A'){if(grant_plan(line,stdout,sizes))return 3;continue;}
        if(line[0]=='W'){if(write_planned_range(&q,line,stdout))return 3;continue;}
        unsigned lun;unsigned long long off,len;char extra;
        if(sscanf(line,"R %u %llu %llu %c",&lun,&off,&len,&extra)!=3||lun>2||!len||len>1048576||off%512||len%512||off>sizes[lun]*512||len>sizes[lun]*512-off)return 3;
        static unsigned char data[1048576];if(firehose_pread(&q,lun,off/512,data,512,len/512)!=(ssize_t)len)return 3;
        if(fwrite(data,1,len,stdout)!=len||fflush(stdout))return 3;
    }
    free(allowed);for(int i=0;i<3;i++)if(q.fd[i]>=0)close(q.fd[i]);return 0;
}
