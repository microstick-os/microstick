/* Included by the pinned QDL build, and by its transport regression harness. */
#include <ctype.h>
#include <inttypes.h>
#include <math.h>
struct firehose_getsize_args { size_t sector_size, num_sectors; };
static bool microstick_geometry_ready(void *data){struct firehose_getsize_args *args=data;return args->sector_size&&args->num_sectors;}
static int microstick_size(size_t *dest, const char *text) {
    while (isspace((unsigned char)*text)) text++;
    if (!isdigit((unsigned char)*text)) return -EINVAL;
    errno=0; char *end; uintmax_t n=strtoumax(text,&end,10);
    while (isspace((unsigned char)*end)) end++;
    if (errno || *end || !n || n>SIZE_MAX || (*dest && *dest!=n)) return -EINVAL;
    *dest=(size_t)n; return 0;
}
static int firehose_getstorageinfo_parser(xmlNode *node, void *data, bool *rawmode __unused) {
    struct firehose_getsize_args *args=data;
    xmlChar *value=xmlGetProp(node,(xmlChar *)"value");
    if (!value) return -EINVAL;
    int ret=0; const char *s=(const char *)value;
    if (!xmlStrcmp(node->name,(xmlChar *)"response")) ret=strcmp(s,"ACK")?-EIO:1;
    else if (!xmlStrcmp(node->name,(xmlChar *)"log")) {
        while (isspace((unsigned char)*s)) s++;
        const char *keys[]={"num_partition_sectors", "SECTOR_SIZE_IN_BYTES"};
        size_t *fields[]={&args->num_sectors,&args->sector_size};
        for (unsigned i=0;i<2;i++) {
            size_t len=strlen(keys[i]);
            if (strncmp(s,keys[i],len)) continue;
            const char *p=s+len; while(isspace((unsigned char)*p))p++;
            if (*p=='=') ret=microstick_size(fields[i],p+1);
        }
        const char *start=strchr(s,'{');
        if (start && strstr(start,"\"storage_info\"")) {
            struct json_value *json=json_parse_buf(start,strlen(start));
            if (!json) ret=-EINVAL;
            else {
                struct json_value *info=json_get_child(json,"storage_info");
                if (info) {
                    double n=0,b=0;
                    json_get_number(info,"total_blocks",&n); json_get_number(info,"block_size",&b);
                    if (!isfinite(n)||!isfinite(b)||n<1||n>(double)(SIZE_MAX/512)||b<1||b>1048576 || n!=(double)(size_t)n||b!=(double)(size_t)b || (args->num_sectors&&args->num_sectors!=(size_t)n)||(args->sector_size&&args->sector_size!=(size_t)b))ret=-EINVAL;
                    else { args->num_sectors=(size_t)n;args->sector_size=(size_t)b; }
                }
                json_free(json);
            }
        }
    }
    xmlFree(value); return ret;
}
