#ifndef MICROSTICK_STATE_H
#define MICROSTICK_STATE_H
#include "event.h"
#include <libmm-glib.h>
cJSON *state_modem(MMModem *modem, MMModem3gpp *gsm);
cJSON *state_bearer(MMBearer *bearer);
cJSON *state_status(MMModem *modem, MMModem3gpp *gsm, gboolean config);
gboolean state_ttl_healthy(void);
gboolean state_ttl_matches(void);
#endif
