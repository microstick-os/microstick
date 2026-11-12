/* Fixed read-only MSM8916/APQ8016 SEC_BOOT probe. No caller-controlled address,
 * arbitrary peek/poke or fuse programming is exposed. Raw bytes stay local.
 */
#ifndef MICROSTICK_EDL_SECURITY_H
#define MICROSTICK_EDL_SECURITY_H
#include <ctype.h>
#include <libxml/parser.h>
static int microstick_security_xml(const char *xml,size_t len,unsigned char value[4],size_t *got,bool *ack) {
    if(len>65536)return -1;
    xmlDoc *doc=xmlReadMemory(xml,(int)len,NULL,NULL,XML_PARSE_NONET|XML_PARSE_NOERROR|XML_PARSE_NOWARNING);
    if(!doc)return -1;
    xmlNode *root=xmlDocGetRootElement(doc);int ret=0;
    if(doc->intSubset||doc->extSubset||!root||root->ns||xmlStrcmp(root->name,(const xmlChar *)"data")){xmlFreeDoc(doc);return -1;}
    for(xmlNode *p=root->children;p && !ret;p=p->next){
        if(p->type==XML_TEXT_NODE){if(!xmlIsBlankNode(p))ret=-1;continue;}
        if(p->type!=XML_ELEMENT_NODE || p->ns || p->children){ret=-1;break;}
        xmlChar *v=xmlGetProp(p,(const xmlChar *)"value");const char *s=(const char *)v;
        if(!xmlStrcmp(p->name,(const xmlChar *)"response")){
            if(!s||strcmp(s,"ACK")||*ack||*got!=4)ret=-1;else *ack=true;
        } else if(!xmlStrcmp(p->name,(const xmlChar *)"log") && s && !*ack){
            if(strcmp(s,"Using address 00058098")){
                if(s[0]!='0' || s[1]!='x')ret=-1;
                while(*s && !ret){
                    if(strlen(s)<4 || *got>=4 || s[0]!='0' || s[1]!='x' || !isxdigit((unsigned char)s[2]) || !isxdigit((unsigned char)s[3])){ret=-1;break;}
                    char pair[3]={s[2],s[3],0};value[(*got)++]=(unsigned char)strtoul(pair,NULL,16);s+=4;
                    if(*s && !isspace((unsigned char)*s)){ret=-1;break;}
                    while(isspace((unsigned char)*s))s++;
                }
            }
        } else ret=-1;
        xmlFree(v);
    }
    xmlFreeDoc(doc);return ret;
}
#ifndef MICROSTICK_SECURITY_PARSER_ONLY
static int microstick_secure_boot(struct qdl_device *q) {
    /* QFPROM SEC_BOOT[0..3]; authentication bit 5 in each byte. Protocol and
     * address reference: bkerler/edl secureboottbl, secureboot and cmd_peek.
     * The pinned programmer requires SizeInBytes (live NAK for size_in_bytes).
     */
    const char *request="<?xml version=\"1.0\"?><data><peek address64=\"0x58098\" SizeInBytes=\"0x4\"/></data>";
    size_t len=strlen(request);
    if(qdl_write(q,request,len,2000)!=(ssize_t)len)return -1;
    char buffer[65537];size_t used=0,got=0;unsigned char value[4]={0};bool ack=false;
    uint64_t deadline=microstick_session_ms()+3000;
    while(microstick_session_ms()<deadline){
        int n=qdl_read(q,buffer+used,sizeof(buffer)-1-used,200);
        if(n==-ETIMEDOUT||n==0)continue;
        if(n<0)return -1;
        used+=(size_t)n;buffer[used]=0;
        for(;;){
            char *start=strchr(buffer,'<'),*end=start?strstr(start,"</data>"):NULL;
            if(!end)break;
            for(char *p=buffer;p<start;p++)if(!isspace((unsigned char)*p))return -1;
            size_t consumed=(size_t)(end-buffer)+7;
            if(microstick_security_xml(start,(size_t)(end-start)+7,value,&got,&ack))return -1;
            used-=consumed;memmove(buffer,buffer+consumed,used);buffer[used]=0;
        }
        if(ack && got==4){
            for(size_t i=0;i<used;i++)if(!isspace((unsigned char)buffer[i]))return -1;
            return ((value[0]|value[1]|value[2]|value[3])&0x20)?1:0;
        }
        if(used==sizeof(buffer)-1)return -1;
    }
    return -1;
}
#endif
#endif
