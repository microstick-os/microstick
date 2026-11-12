#ifndef MICROSTICK_APN_H
#define MICROSTICK_APN_H
#include <cjson/cJSON.h>
cJSON *apn_catalog(void);
cJSON *apn_resolve(const cJSON *catalog, const cJSON *config, const char *home,
                   const char *spn, const char *gid1);
cJSON *apn_profile_list(const cJSON *catalog);
#endif
