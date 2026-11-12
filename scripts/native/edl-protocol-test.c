/* Actual patched QDL getsize/read functions, with a deterministic transport. */
#include <assert.h>
#include "firehose.c"
#include "edl-session.h"
bool qdl_debug;
int vip_transfer_handle_tables(struct qdl_device *q){(void)q;return 0;}
bool vip_transfer_status_check_needed(struct qdl_device *q){(void)q;return false;}
void vip_transfer_clear_status(struct qdl_device *q){(void)q;}
void vip_gen_chunk_init(struct qdl_device *q){(void)q;}
void vip_gen_chunk_update(struct qdl_device *q,const void *b,size_t n){(void)q;(void)b;(void)n;}
void vip_gen_chunk_store(struct qdl_device *q){(void)q;}
static const char *input;
static size_t input_len,input_pos,fragment=4096;
static bool wait_write,disconnect;
static unsigned writes;
static unsigned idle_reads;
static size_t pause_at;static bool paused;
int qdl_read(struct qdl_device *q,void *buf,size_t len,unsigned timeout) {
    (void)timeout;
    if(q->pending_len){size_t n=q->pending_len;if(n>len)n=len;memcpy(buf,q->pending_buf,n);memmove(q->pending_buf,q->pending_buf+n,q->pending_len-n);q->pending_len-=n;return (int)n;}
    if(wait_write)return -ETIMEDOUT;
    if(pause_at&&input_pos==pause_at&&!paused){paused=true;return -ETIMEDOUT;}
    if(input_pos==input_len){idle_reads++;return disconnect?-ENODEV:-ETIMEDOUT;}
    size_t n=input_len-input_pos;if(n>fragment)n=fragment;if(n>len)n=len;
    if(pause_at&&input_pos<pause_at&&n>pause_at-input_pos)n=pause_at-input_pos;
    memcpy(buf,input+input_pos,n);input_pos+=n;return (int)n;
}
int qdl_write(struct qdl_device *q,const void *buf,size_t n,unsigned timeout) {
    (void)q;(void)buf;(void)timeout;writes++;wait_write=false;return (int)n;
}
int qdl_push_back(struct qdl_device *q,const void *buf,size_t len) {
    assert(!q->pending_len);q->pending_buf=malloc(len);assert(q->pending_buf);memcpy(q->pending_buf,buf,len);q->pending_len=len;return 0;
}
static struct qdl_device device;
static void reset(const char *data) {
    free(device.pending_buf);memset(&device,0,sizeof(device));
    input=data;input_len=strlen(data);input_pos=0;writes=0;idle_reads=0;wait_write=false;disconnect=false;fragment=4096;pause_at=0;paused=false;
}
static int geometry(const char *s,size_t expected,bool valid) {
    size_t ss=0,sectors=0;reset(s);int ret=firehose_getsize(&device,0,&ss,&sectors);
    assert((ret==0)==valid);if(valid){assert(ss==512);assert(sectors==expected);}return 0;
}
int main(int argc,char **argv) {
    if(argc==3){FILE *f=fopen(argv[1],"rb");assert(f);char b[8192];size_t n=fread(b,1,sizeof(b)-1,f);fclose(f);b[n]=0;
        size_t splits[]={1,7,17,4096,65536};
        for(unsigned i=0;i<sizeof(splits)/sizeof(splits[0]);i++) {reset(b);fragment=splits[i];size_t ss=0,sectors=0;assert(!firehose_getsize(&device,atoi(argv[2]),&ss,&sectors));assert(ss==512);assert(sectors==(!strcmp(argv[2],"0")?7634944u:8192u));}
        free(device.pending_buf);puts("PASS real fixture: all transport fragment sizes");return 0;
    }
    const char *ack="<?xml version=\"1.0\"?><data><response value=\"ACK\"/></data>";
    geometry("<data><log value=\"num_partition_sectors=8192\"/><log value=\"SECTOR_SIZE_IN_BYTES=512\"/><response value=\"ACK\"/></data>",8192,true);
    geometry("<data><response value=\"ACK\"/></data><data><log value=\"SECTOR_SIZE_IN_BYTES=512\"/><log value=\"num_partition_sectors=8192\"/></data>",8192,true);
    const char *late="<data><response value='ACK'/></data><data><log value='SECTOR_SIZE_IN_BYTES=512'/><log value='num_partition_sectors=8192'/></data>";
    reset(late);pause_at=strlen("<data><response value='ACK'/></data>");size_t delayed_size=0,delayed_sectors=0;
    assert(!firehose_getsize(&device,0,&delayed_size,&delayed_sectors));assert(paused&&delayed_size==512&&delayed_sectors==8192);
    geometry("<data><log value='{\"storage_info\": {\"total_blocks\": 8192, \"block_size\": 512}}'/><response value='ACK'/></data>",8192,true);
    const char *bad[]={"0","-1","18446744073709551616","12junk","1.5"};
    for(unsigned i=0;i<sizeof(bad)/sizeof(bad[0]);i++){char b[512];snprintf(b,sizeof(b),"<data><log value='num_partition_sectors=%s'/><log value='SECTOR_SIZE_IN_BYTES=512'/><response value='ACK'/></data>",bad[i]);geometry(b,0,false);}
    geometry(ack,0,false);
    geometry("<data><log value='num_partition_sectors=8'/><log value='num_partition_sectors=9'/><log value='SECTOR_SIZE_IN_BYTES=512'/><response value='ACK'/></data>",0,false);
    geometry("<data><log value='num_partition_sectors=8'/><log value='SECTOR_SIZE_IN_BYTES=512'/><response value='NAK'/><response value='ACK'/></data>",0,false);
    reset("<data><log value='hello'/><response value='ACK' rawmode='true'/></data>RAW!");bool raw=false;(void)raw;
    assert(firehose_read(&device,100,firehose_generic_parser,NULL)==0);char tail[4];assert(qdl_read(&device,tail,4,0)==4&&!memcmp(tail,"RAW!",4));
    const char *prefix="<data><response value='ACK' rawmode='true'/></data>";
    size_t length=1048576,head=strlen(prefix),ending=strlen(ack);
    char *stream=malloc(head+length+ending),*payload=malloc(length);assert(stream&&payload);
    memcpy(stream,prefix,head);memset(stream+head,0x5a,length);memcpy(stream+head+length,ack,ending);
    reset("");input=stream;input_len=head+length+ending;fragment=16384;device.max_payload_size=16384;
    assert(firehose_pread(&device,0,0,payload,512,length/512)==(ssize_t)length);assert(writes==1 && idle_reads==0);
    for(size_t i=0;i<length;i++)assert((unsigned char)payload[i]==0x5a);
    free(stream);free(payload);
    reset(ack);wait_write=true;assert(microstick_session(&device)==1&&writes==1);
    reset("<data><response value='NAK'/></data>");wait_write=true;assert(microstick_session(&device)<0);
    reset("<data><response");assert(firehose_read(&device,5,firehose_generic_parser,NULL)<0);
    reset(ack);disconnect=true;assert(firehose_read(&device,100,firehose_generic_parser,NULL)==0);
    /* Once the complete ACK has arrived, do not poll for silence/disconnect. */
    reset("");disconnect=true;assert(firehose_read(&device,100,firehose_generic_parser,NULL)<0);
    unsigned char hello[48]={1,0,0,0,48};
    for(size_t split=1;split<=48;split++){reset("");fragment=split;input=(char *)hello;input_len=48;assert(microstick_session(&device)==0&&writes==0);assert(qdl_read(&device,tail,4,0)==4&&tail[0]==1);}
    free(device.pending_buf);puts("PASS parser, framing, raw tail, Sahara and warm Firehose");return 0;
}
