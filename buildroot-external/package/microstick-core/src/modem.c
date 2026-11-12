#include <cjson/cJSON.h>
#include <libmm-glib.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
#include "apn.h"
static const char *value(cJSON *j, const char *key) {
  cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsString(v) ? v->valuestring : "";
}
/* Return only known codes. Remote messages can contain credentials. */
static const char *error_code(GError *error, gboolean pin) {
  if (!error)
    return pin ? "pin_not_required" : "lte_connect_failed";
  gchar *remote = g_dbus_error_get_remote_error(error);
  gboolean not_ready = remote && !strcmp(remote,
      "org.freedesktop.libqmi.Error.Protocol.DeviceNotReady");
  g_free(remote);
  if (not_ready)
    return "modem_not_ready";
  if (g_error_matches(error, MM_CORE_ERROR, MM_CORE_ERROR_IN_PROGRESS))
    return "modem_busy";
  if (g_error_matches(error, MM_MOBILE_EQUIPMENT_ERROR,
                      MM_MOBILE_EQUIPMENT_ERROR_SIM_PIN))
    return "sim_pin_required";
  if (g_error_matches(error, MM_MOBILE_EQUIPMENT_ERROR,
                      MM_MOBILE_EQUIPMENT_ERROR_SIM_PUK))
    return "sim_puk_required";
  if (g_error_matches(error, MM_MOBILE_EQUIPMENT_ERROR,
                      MM_MOBILE_EQUIPMENT_ERROR_NO_NETWORK))
    return "lte_no_network";
  if (g_error_matches(error, MM_MOBILE_EQUIPMENT_ERROR,
                      MM_MOBILE_EQUIPMENT_ERROR_NETWORK_NOT_ALLOWED))
    return "registration_denied";
  if (g_error_matches(error, MM_MOBILE_EQUIPMENT_ERROR,
                      MM_MOBILE_EQUIPMENT_ERROR_NETWORK_TIMEOUT) ||
      g_error_matches(error, G_IO_ERROR, G_IO_ERROR_TIMED_OUT))
    return "lte_timeout";
  return pin ? "pin_failed" : "lte_connect_failed";
}
int main(int argc, char **argv) {
  if (argc != 2)
    return 2;
  char buffer[4096] = {0};
  size_t n = fread(buffer, 1, sizeof(buffer) - 1, stdin);
  if (!n || n == sizeof(buffer) - 1)
    return 2;
  cJSON *j = cJSON_Parse(buffer);
  if (!j)
    return 2;
  GError *error = NULL;
  GDBusConnection *bus = g_bus_get_sync(G_BUS_TYPE_SYSTEM, NULL, &error);
  MMManager *manager =
      bus ? mm_manager_new_sync(
                bus, G_DBUS_OBJECT_MANAGER_CLIENT_FLAGS_DO_NOT_AUTO_START, NULL,
                &error)
          : NULL;
  GList *objects =
      manager
          ? g_dbus_object_manager_get_objects(G_DBUS_OBJECT_MANAGER(manager))
          : NULL;
  MMModem *modem =
      objects ? mm_object_get_modem(MM_OBJECT(objects->data)) : NULL;
  gboolean ok = FALSE;
  const char *failure = NULL;
  if (!strcmp(argv[1], "profile")) {
    cJSON *catalog=apn_catalog();
    MMSim *sim=modem ? mm_modem_get_sim_sync(modem,NULL,NULL) : NULL;
    char *gid=NULL;
    if (sim) {
      gsize count=0; const guint8 *bytes=mm_sim_get_gid1(sim,&count);
      GString *hex=g_string_new("");
      for (gsize i=0;bytes && i<count;i++) g_string_append_printf(hex,"%02X",bytes[i]);
      gid=g_string_free(hex,FALSE);
    }
    cJSON *out=apn_resolve(catalog,j,sim ? mm_sim_get_operator_identifier(sim) : NULL,
                          sim ? mm_sim_get_operator_name(sim) : NULL,gid);
    ok=cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(out,"ok"));
    char *encoded=cJSON_PrintUnformatted(out);
    puts(encoded); free(encoded); cJSON_Delete(out); cJSON_Delete(catalog);
    g_free(gid); g_clear_object(&sim);
  } else if (modem && !strcmp(argv[1], "pin")) {
    const char *pin = value(j, "pin");
    size_t len = strlen(pin);
    gboolean valid = len >= 4 && len <= 8;
    for (size_t i = 0; i < len; i++)
      if (pin[i] < '0' || pin[i] > '9')
        valid = FALSE;
    if (!valid)
      failure = "invalid_pin";
    if (valid && mm_modem_get_unlock_required(modem) == MM_MODEM_LOCK_SIM_PIN) {
      MMSim *sim = mm_modem_get_sim_sync(modem, NULL, &error);
      if (sim) {
        ok = mm_sim_send_pin_sync(sim, pin, NULL, &error);
        g_object_unref(sim);
      }
    }
  } else if (modem && !strcmp(argv[1], "connect")) {
    MMModemSimple *simple =
        mm_object_get_modem_simple(MM_OBJECT(objects->data));
    MMSimpleConnectProperties *p = mm_simple_connect_properties_new();
    mm_simple_connect_properties_set_apn(p, value(j, "apn"));
    mm_simple_connect_properties_set_ip_type(p, MM_BEARER_IP_FAMILY_IPV4);
    mm_simple_connect_properties_set_user(p, value(j, "username"));
    mm_simple_connect_properties_set_password(p, value(j, "password"));
    const char *auth = value(j, "auth");
    MMBearerAllowedAuth a = MM_BEARER_ALLOWED_AUTH_UNKNOWN;
    if (!strcmp(auth, "none"))
      a = MM_BEARER_ALLOWED_AUTH_NONE;
    else if (!strcmp(auth, "pap"))
      a = MM_BEARER_ALLOWED_AUTH_PAP;
    else if (!strcmp(auth, "chap"))
      a = MM_BEARER_ALLOWED_AUTH_CHAP;
    else if (!strcmp(auth, "pap-or-chap"))
      a = MM_BEARER_ALLOWED_AUTH_PAP | MM_BEARER_ALLOWED_AUTH_CHAP;
    mm_simple_connect_properties_set_allowed_auth(p, a);
    if (simple) {
      g_dbus_proxy_set_default_timeout(G_DBUS_PROXY(simple), 110000);
      MMBearer *b = mm_modem_simple_connect_sync(simple, p, NULL, &error);
      if (b) {
        ok = TRUE;
        g_object_unref(b);
      }
      g_object_unref(simple);
    }
    g_object_unref(p);
  }
  if (strcmp(argv[1],"profile")) printf("{\"ok\":%s,\"error\":\"%s\"}\n", ok ? "true" : "false",
         ok ? "" : !modem ? "modem_unavailable" : failure ? failure :
         error_code(error, !strcmp(argv[1], "pin")));
  /* Do not print remote errors: they may include operator credentials. */
  if (error)
    g_error_free(error);
  if (modem)
    g_object_unref(modem);
  g_list_free_full(objects, g_object_unref);
  if (manager)
    g_object_unref(manager);
  if (bus)
    g_object_unref(bus);
  cJSON_Delete(j);
  memset(buffer, 0, sizeof(buffer));
  return ok ? 0 : 1;
}
