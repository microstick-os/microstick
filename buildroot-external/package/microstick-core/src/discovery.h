#ifndef MICROSTICK_DISCOVERY_H
#define MICROSTICK_DISCOVERY_H
#include "event.h"
void discovery_init(EvChanged changed);
void discovery_tick(void);
cJSON *discovery_status(void);
#endif
