/* Exact-range authorization protocol shared by the native EDL bridge and its
 * file-backed harness. Initial sessions expose I/R only; a bounded grant is
 * accepted only after the Go engine's complete preflight. No reset/erase exists.
 */
#ifndef MICROSTICK_EDL_PLAN_H
#define MICROSTICK_EDL_PLAN_H
#include "sha2.h"
struct allowed_range { unsigned lun; uint64_t off,len; char before[65],after[65]; bool done; };
static struct allowed_range *allowed;
static size_t allowed_count;
static size_t next_write_index;
static bool payload_v2;
static char approved_plan[65];
static SHA2_CTX input_hash;
static FILE *progress_wire;
static const char *progress_phase;
static int plan_error(FILE *wire,const char *stage){fprintf(wire,"E %s\n",stage);fflush(wire);return -1;}
static void hash_text(SHA2_CTX *hash,char out[65]) { unsigned char digest[32]; const char *hex="0123456789abcdef";SHA256Final(digest,hash);for(size_t i=0;i<32;i++){out[2*i]=hex[digest[i]>>4];out[2*i+1]=hex[digest[i]&15];}out[64]=0; }
static bool digest_text(const char *s) { if(strlen(s)!=64)return false;for(size_t i=0;i<64;i++)if(!((s[i]>='0'&&s[i]<='9')||(s[i]>='a'&&s[i]<='f')))return false;return true; }
int microstick_next_write(void *buf,size_t len) {
    if(len>16384 || fread(buf,1,len,stdin)!=len)return -1;
    SHA256Update(&input_hash,buf,len);return 0;
}
static int hash_range(struct qdl_device *q,unsigned lun,uint64_t off,uint64_t len,char out[65]) {
    static unsigned char buf[1048576];SHA2_CTX hash;SHA256Init(&hash);
    uint64_t total=len;
    while(len){size_t n=len>sizeof(buf)?sizeof(buf):(size_t)len;
        if(firehose_pread(q,lun,off/512,buf,512,n/512)!=(ssize_t)n)return -1;
        SHA256Update(&hash,buf,n);off+=n;len-=n;
        if(progress_wire && (!len || (total-len)%(4*1048576)==0)){
            fprintf(progress_wire,"P %s %" PRIu64 " %" PRIu64 "\n",progress_phase,total-len,total);if(fflush(progress_wire))return -1;
        }
    }
    hash_text(&hash,out);return 0;
}
static int compare_range(const void *ap,const void *bp) {
    const struct allowed_range *a=ap,*b=bp;
    if(a->lun!=b->lun)return a->lun<b->lun?-1:1;
    return a->off==b->off?0:(a->off<b->off?-1:1);
}
static int grant_plan(const char *line,FILE *wire,size_t sizes[3]) {
    char token[65],extra;unsigned n;
    payload_v2=!strncmp(line,"A2 ",3);
    if(allowed || sscanf(line+(payload_v2?3:2),"%64s %u %c",token,&n,&extra)!=2 || !digest_text(token) || !n || n>65536)return -1;
    allowed=calloc(n,sizeof(*allowed));if(!allowed)return -1;
    char entry[256];
    for(unsigned i=0;i<n;i++){
        struct allowed_range *r=&allowed[i];unsigned long long off,len;
        if(!fgets(entry,sizeof(entry),stdin)||sscanf(entry,"%u %llu %llu %64s %64s %c",&r->lun,&off,&len,r->before,r->after,&extra)!=5)return -1;
        r->off=off;r->len=len;
        if(r->lun>2||!len||off%512||len%512||off>sizes[r->lun]*512||len>sizes[r->lun]*512-off||(!digest_text(r->before)&&!(payload_v2&&!strcmp(r->before,"-")))||!digest_text(r->after))return -1;
    }
    struct allowed_range *sorted=malloc(n*sizeof(*sorted));if(!sorted)return -1;
    memcpy(sorted,allowed,n*sizeof(*sorted));qsort(sorted,n,sizeof(*sorted),compare_range);
    for(unsigned i=1;i<n;i++)if(sorted[i-1].lun==sorted[i].lun&&sorted[i-1].off+sorted[i-1].len>sorted[i].off){free(sorted);return -1;}
    free(sorted);memcpy(approved_plan,token,65);allowed_count=n;
    fputs("ARMED\n",wire);return fflush(wire);
}
static int write_planned_range(struct qdl_device *q,const char *line,FILE *wire) {
    char token[65],extra,digest[65];unsigned index;
    if(sscanf(line,"W %64s %u %c",token,&index,&extra)!=2||!allowed||strcmp(token,approved_plan)||index>=allowed_count||index!=next_write_index||allowed[index].done)return -1;
    struct allowed_range *r=&allowed[index];
    progress_wire=wire;progress_phase="source";
    if(!payload_v2){
      if(hash_range(q,r->lun,r->off,r->len,digest))return plan_error(wire,"source-read");
      if(strcmp(digest,r->before))return plan_error(wire,"source-changed");
    }
    if(!strcmp(r->before,r->after)){
        r->done=true;next_write_index++;
        fprintf(wire,"VERIFIED %" PRIu64 " %s\n",r->len,r->after);return fflush(wire);
    }
    fputs("READY\n",wire);if(fflush(wire))return -1;
    SHA256Init(&input_hash);
    if(firehose_pwrite(q,r->lun,r->off/512,NULL,512,r->len/512)!=(ssize_t)r->len)return plan_error(wire,"write-transfer");
    hash_text(&input_hash,digest);if(strcmp(digest,r->after))return plan_error(wire,"input-hash");
    progress_phase="readback";
    if(hash_range(q,r->lun,r->off,r->len,digest))return plan_error(wire,"readback-read");
    if(strcmp(digest,r->after))return plan_error(wire,"readback-hash");
    r->done=true;next_write_index++;fprintf(wire,"DONE %" PRIu64 " %s\n",r->len,digest);return fflush(wire);
}
#endif
