/* A passive HELLO is never lost to Firehose probing. No target reset. */
#include <libxml/parser.h>
#include <time.h>
static uint64_t microstick_session_ms(void){struct timespec t;clock_gettime(CLOCK_MONOTONIC,&t);return (uint64_t)t.tv_sec*1000+t.tv_nsec/1000000;}
static bool microstick_hello(const unsigned char *b,size_t n) {
    return n>=8 && b[0]==1 && !b[1] && !b[2] && !b[3] && b[4]==48 && !b[5] && !b[6] && !b[7];
}
static int microstick_session(struct qdl_device *q) {
    unsigned char buf[65537];size_t used=0;
    int n=qdl_read(q,buf,sizeof(buf)-1,1000);
    if(n<0 && n!=-ETIMEDOUT)return -1;
    if(n>0) {
        used=(size_t)n;
        /* A binary HELLO can itself span transport reads. Do not send XML
         * while collecting it, and return its complete header to Sahara. */
        if(buf[0]==1) {
            uint64_t hello_deadline=microstick_session_ms()+2500;
            for(unsigned tries=0;used<48&&tries<128&&microstick_session_ms()<hello_deadline;tries++){
                n=qdl_read(q,buf+used,48-used,100);if(n==-ETIMEDOUT)continue;if(n<=0)return -1;used+=(size_t)n;
            }
            if(used<48||!microstick_hello(buf,used))return -1;
            return qdl_push_back(q,buf,used)<0?-1:0;
        }
        buf[used]=0;
    }
    /* Initial XML is a banner, not proof that our nop was acknowledged. */
    const char *nop="<?xml version=\"1.0\"?><data><nop/></data>";
    if(qdl_write(q,nop,strlen(nop),2000)<0)return -1;
    uint64_t deadline=microstick_session_ms()+2500;
    for(unsigned tries=0;tries<65536&&microstick_session_ms()<deadline;tries++) {
        n=qdl_read(q,buf+used,sizeof(buf)-1-used,100);
        if(n==-ETIMEDOUT||n==0){if(tries>20)break;continue;}
        if(n<0)return -1;
        used+=(size_t)n;buf[used]=0;
        if(microstick_hello(buf,used)&&used>=48)return qdl_push_back(q,buf,used)<0?-1:0;
        for(;;) {
            char *start=strchr((char *)buf,'<'),*end=start?strstr(start,"</data>"):NULL;
            if(!end)break;
            size_t length=(size_t)(end-start)+7,consumed=(size_t)(end-(char *)buf)+7;
            xmlDoc *doc=xmlReadMemory(start,(int)length,NULL,NULL,XML_PARSE_NONET|XML_PARSE_NOERROR|XML_PARSE_NOWARNING);
            if(!doc)return -1;
            xmlNode *root=xmlDocGetRootElement(doc);int ack=0;
            if(!doc->intSubset&&!doc->extSubset&&root&&!xmlStrcmp(root->name,(xmlChar *)"data"))
                for(xmlNode *p=root->children;p;p=p->next)if(!xmlStrcmp(p->name,(xmlChar *)"response")) {
                    xmlChar *v=xmlGetProp(p,(xmlChar *)"value");ack=v&&!xmlStrcmp(v,(xmlChar *)"ACK")?1:-1;xmlFree(v);
                }
            xmlFreeDoc(doc);used-=consumed;memmove(buf,buf+consumed,used);buf[used]=0;
            if(ack)return ack;
        }
        if(used==sizeof(buf)-1)return -1;
    }
    /* A quiet Sahara with an already-consumed HELLO is recovered by the
     * existing command-mode handshake. Silence alone never proves Firehose. */
    return used?-1:0;
}
