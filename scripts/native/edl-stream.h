/* Bound XML documents independently of transport packets. Preserve raw tails. */
#include <time.h>
#include <ctype.h>
static bool (*microstick_response_ready)(void *data);
static uint64_t microstick_ms(void) {
    struct timespec t; clock_gettime(CLOCK_MONOTONIC,&t);
    return (uint64_t)t.tv_sec*1000+t.tv_nsec/1000000;
}
static int firehose_read(struct qdl_device *qdl,int timeout_ms,
        int (*parser)(xmlNode *,void *,bool *),void *data) {
    char buf[65537];size_t used=0;int response=0,result=0;
    uint64_t deadline=microstick_ms()+(unsigned)timeout_ms;
    for (;;) {
        if (microstick_ms()>=deadline)return response&&!used&&(!microstick_response_ready||microstick_response_ready(data))?result:-ETIMEDOUT;
        int n=qdl_read(qdl,buf+used,sizeof(buf)-1-used,100);
        if ((n==-ETIMEDOUT || n==0) && response && !used&&(!microstick_response_ready||microstick_response_ready(data)))return result;
        if(n==-ETIMEDOUT || n==0)continue;
        if(n<0)return n;
        used+=(size_t)n;buf[used]=0;
        for (;;) {
            size_t skip=0;while(skip<used&&(isspace((unsigned char)buf[skip])||buf[skip]==0))skip++;
            if(skip){memmove(buf,buf+skip,used-skip);used-=skip;buf[used]=0;}
            if(!used)break;
            char *end=strstr(buf,"</data>");
            if(!end){if(used==sizeof(buf)-1)return -EOVERFLOW;break;}
            size_t len=(size_t)(end-buf)+7;
            xmlDoc *doc=xmlReadMemory(buf,(int)len,NULL,NULL,XML_PARSE_NONET|XML_PARSE_NOERROR|XML_PARSE_NOWARNING);
            if(!doc)return -EINVAL;
            xmlNode *root=xmlDocGetRootElement(doc);bool raw=false;int status=0;
            if(!root||xmlStrcmp(root->name,(xmlChar *)"data")||doc->intSubset||doc->extSubset){xmlFreeDoc(doc);return -EINVAL;}
            for(xmlNode *node=root->children;node;node=node->next) {
                if(node->type!=XML_ELEMENT_NODE)continue;
                firehose_check_vip_marker(qdl,node);
                int r=parser(node,data,&raw);
                if(r<0 && !(r==-EAGAIN&&!xmlStrcmp(node->name,(xmlChar *)"log"))){status=r;break;}
                /* Some upstream parsers return zero for ACK. Inspect the tag,
                 * not the last log callback, to retain the response status. */
                if(!xmlStrcmp(node->name,(xmlChar *)"response")) {
                    xmlChar *v=xmlGetProp(node,(xmlChar *)"value");
                    if(!v)status=-EIO;
                    else if(!xmlStrcmp(v,(xmlChar *)"ACK")){response=1;result=r;}
                    else if(!xmlStrcmp(v,(xmlChar *)"NAK") && r==0 && xmlHasProp(node,(xmlChar *)"MaxPayloadSizeToTargetInBytes")){response=1;result=r;}
                    else status=-EIO;
                    xmlFree(v);
                    if(status<0)break;
                }
            }
            xmlFreeDoc(doc);if(status<0)return status;
            used-=len;memmove(buf,buf+len,used);buf[used]=0;
            if(raw){if(used&&qdl_push_back(qdl,buf,used)<0)return -ENOMEM;return response?result:-EPROTO;}
        }
        /* A complete command ACK ends the exchange. Waiting for an extra USB
         * timeout here costs 100ms for every logical read. Storage discovery
         * alone needs the bounded wait for separately delivered geometry logs. */
        if(response && !used && !microstick_response_ready)return result;
    }
}
