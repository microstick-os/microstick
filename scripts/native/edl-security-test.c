/* In-memory wire tests. No USB or register access is linked. */
#include <stdio.h>
#include <stdlib.h>
#include <stdint.h>
#include <stdbool.h>
#include <string.h>
#include <unistd.h>
#include <errno.h>
#include <assert.h>
struct qdl_device { const char *wire;size_t pos,chunk;uint64_t clock; };
static uint64_t ticks;
static uint64_t microstick_session_ms(void){return ticks++;}
static ssize_t qdl_write(struct qdl_device *q,const void *b,size_t n,int timeout){
    (void)q;(void)timeout;
    assert(!strcmp(b,"<?xml version=\"1.0\"?><data><peek address64=\"0x58098\" SizeInBytes=\"0x4\"/></data>"));
    return (ssize_t)n;
}
static int qdl_read(struct qdl_device *q,void *b,size_t n,int timeout){
    (void)timeout;size_t remain=strlen(q->wire)-q->pos;
    if(!remain){ticks+=200;return -ETIMEDOUT;}
    if(n>q->chunk)n=q->chunk;
    if(n>remain)n=remain;
    memcpy(b,q->wire+q->pos,n);q->pos+=n;return (int)n;
}
#include "edl-security.h"
#define DATA "<?xml version=\"1.0\"?><data><log value=\"0x00 0x00 0x00 0x00 \"/></data>"
#define ACK "<?xml version=\"1.0\"?><data><response value=\"ACK\"/></data>"
static void check(const char *wire,int want,size_t chunk){
    struct qdl_device q={wire,0,chunk,0};ticks=0;int got=microstick_secure_boot(&q);assert(got==want);
}
int main(void){
    for(size_t chunk=1;chunk<=512;chunk*=2){
        check("<?xml version=\"1.0\"?><data><log value=\"Using address 00058098\"/></data>" DATA ACK,0,chunk);
        check("<data><log value=\"0x00 0x00 0x00 0x20\"/></data>" ACK,1,chunk);
        check(DATA "<data><response value=\"NAK\"/></data>",-1,chunk);
        check(ACK DATA,-1,chunk);
        check(DATA,-1,chunk);
        check("<data><log value=\"0x00 0x00 0x00\"/></data>" ACK,-1,chunk);
        check("<data><log value=\"0x00 0x00 0x00 0x00 0x00\"/></data>" ACK,-1,chunk);
        check("<data><log value=\"0x00z 0x00 0x00 0x00\"/></data>" ACK,-1,chunk);
        check("<!DOCTYPE data [<!ENTITY x SYSTEM 'file:///etc/passwd'>]><data><log value=\"&x;\"/></data>" DATA ACK,-1,chunk);
        check("<data><log value=\"Using address 00058099\"/></data>" DATA ACK,-1,chunk);
        check("<data><log value=\"Invalid parameters\"/></data>" DATA ACK,-1,chunk);
        check("<data><log value=\"0x00 0x00 0x00 0x00\"></data>" ACK,-1,chunk);
    }
    check(DATA ACK "garbage",-1,512);
    puts("PASS secure probe: fragmented ACK/NAK, exact bytes, address, XML/DTD rejection");
    return 0;
}
