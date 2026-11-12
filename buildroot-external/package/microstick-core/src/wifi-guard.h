#ifndef MICROSTICK_WIFI_GUARD_H
#define MICROSTICK_WIFI_GUARD_H
#include "event.h"
void wifi_guard_init(EvChanged changed);
void wifi_guard_tick(const char *mode, const char *iface, gboolean ready);
char *wifi_mode(void);
char *wifi_config(void);
gboolean wifi_ready(const char *iface, const char *status);
cJSON *wifi_confirm(cJSON *q);
cJSON *wifi_guard_status(void);
#endif
