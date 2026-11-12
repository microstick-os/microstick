#ifndef MICROSTICK_SSID_H
#define MICROSTICK_SSID_H
#include <glib.h>
/* iw uses only hex escapes; wpa control also escapes quotes/backslashes. */
char *ssid_display(const char *escaped, gboolean iw, char hex[65]);
#endif
