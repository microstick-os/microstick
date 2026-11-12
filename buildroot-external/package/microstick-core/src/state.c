#include "state.h"
#include "apn.h"
#include "ssid.h"
#include "wifi-guard.h"
#include "discovery.h"
#include <arpa/inet.h>
#include <libmnl/libmnl.h>
#include <libnftnl/expr.h>
#include <libnftnl/rule.h>
#include <libnftnl/table.h>
#include <linux/netfilter.h>
#include <linux/netfilter/nf_tables.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <unistd.h>
static void field(cJSON *j, const char *key, const char *value) {
  cJSON_AddStringToObject(j, key, value && *value ? value : "--");
}
static void strings(cJSON *j, const char *key, const char *const *values) {
  cJSON *a = cJSON_AddArrayToObject(j, key);
  if (values)
    for (int i = 0; values[i]; i++)
      cJSON_AddItemToArray(a, cJSON_CreateString(values[i]));
}
static void mask_strings(cJSON *j, const char *key, char *s) {
  cJSON *a = cJSON_AddArrayToObject(j, key);
  gchar **parts = g_strsplit(s ? s : "", ", ", -1);
  for (int i = 0; parts[i]; i++)
    if (*parts[i])
      cJSON_AddItemToArray(a, cJSON_CreateString(parts[i]));
  g_strfreev(parts);
  g_free(s);
}
cJSON *state_modem(MMModem *m, MMModem3gpp *gsm) {
  cJSON *out = cJSON_CreateObject();
  if (!m)
    return out;
  cJSON *root = cJSON_AddObjectToObject(out, "modem"),
        *g = cJSON_AddObjectToObject(root, "generic");
  field(root, "dbus-path", mm_modem_get_path(m));
  field(g, "state", mm_modem_state_get_string(mm_modem_get_state(m)));
  field(g, "state-failed-reason",
        mm_modem_get_state_failed_reason(m) == MM_MODEM_STATE_FAILED_REASON_NONE
            ? "--"
            : mm_modem_state_failed_reason_get_string(
                  mm_modem_get_state_failed_reason(m)));
  field(g, "unlock-required",
        mm_modem_lock_get_string(mm_modem_get_unlock_required(m)));
  field(g, "power-state",
        mm_modem_power_state_get_string(mm_modem_get_power_state(m)));
  field(g, "manufacturer", mm_modem_get_manufacturer(m));
  field(g, "model", mm_modem_get_model(m));
  field(g, "revision", mm_modem_get_revision(m));
  field(g, "hardware-revision", mm_modem_get_hardware_revision(m));
  field(g, "equipment-identifier", mm_modem_get_equipment_identifier(m));
  field(g, "device-identifier", mm_modem_get_device_identifier(m));
  field(g, "device", mm_modem_get_device(m));
  field(g, "physdev", mm_modem_get_physdev(m));
  field(g, "plugin", mm_modem_get_plugin(m));
  field(g, "primary-port", mm_modem_get_primary_port(m));
  field(g, "sim", mm_modem_get_sim_path(m));
  field(g, "carrier-configuration", mm_modem_get_carrier_configuration(m));
  field(g, "carrier-configuration-revision",
        mm_modem_get_carrier_configuration_revision(m));
  strings(g, "drivers", mm_modem_get_drivers(m));
  strings(g, "bearers", mm_modem_get_bearer_paths(m));
  strings(g, "own-numbers", mm_modem_get_own_numbers(m));
  strings(g, "sim-slots", mm_modem_get_sim_slot_paths(m));
  char n[32];
  guint slot = mm_modem_get_primary_sim_slot(m);
  snprintf(n, sizeof(n), "%u", slot);
  field(g, "primary-sim-slot", slot ? n : "--");
  mask_strings(g, "access-technologies",
               mm_modem_access_technology_build_string_from_mask(
                   mm_modem_get_access_technologies(m)));
  cJSON *signal = cJSON_AddObjectToObject(g, "signal-quality");
  gboolean recent;
  guint quality = mm_modem_get_signal_quality(m, &recent);
  snprintf(n, sizeof(n), "%u", quality);
  field(signal, "value", n);
  field(signal, "recent", recent ? "yes" : "no");
  cJSON *retries = cJSON_AddArrayToObject(g, "unlock-retries");
  MMUnlockRetries *ur = mm_modem_get_unlock_retries(m);
  if (ur) {
    for (int i = MM_MODEM_LOCK_SIM_PIN; i <= MM_MODEM_LOCK_SIM_PUK2; i++) {
      guint v = mm_unlock_retries_get(ur, (MMModemLock)i);
      if (v != MM_UNLOCK_RETRIES_UNKNOWN) {
        char b[80];
        snprintf(b, sizeof(b), "%s (%u)",
                 mm_modem_lock_get_string((MMModemLock)i), v);
        cJSON_AddItemToArray(retries, cJSON_CreateString(b));
      }
    }
    g_object_unref(ur);
  }
  MMModemCapability *caps = NULL;
  guint len = 0;
  cJSON *a = cJSON_AddArrayToObject(g, "supported-capabilities");
  if (mm_modem_get_supported_capabilities(m, &caps, &len)) {
    for (guint i = 0; i < len; i++) {
      char *s = mm_modem_capability_build_string_from_mask(caps[i]);
      cJSON_AddItemToArray(a, cJSON_CreateString(s));
      g_free(s);
    }
    g_free(caps);
  }
  a = cJSON_AddArrayToObject(g, "current-capabilities");
  char *s = mm_modem_capability_build_string_from_mask(
      mm_modem_get_current_capabilities(m));
  cJSON_AddItemToArray(a, cJSON_CreateString(s));
  g_free(s);
  for (int which = 0; which < 2; which++) {
    MMModemBand *bands = NULL;
    len = 0;
    a = cJSON_AddArrayToObject(g, which ? "current-bands" : "supported-bands");
    gboolean ok = which ? mm_modem_get_current_bands(m, &bands, &len)
                        : mm_modem_get_supported_bands(m, &bands, &len);
    if (ok) {
      for (guint i = 0; i < len; i++)
        cJSON_AddItemToArray(
            a, cJSON_CreateString(mm_modem_band_get_string(bands[i])));
      g_free(bands);
    }
  }
  MMModemMode allowed, preferred;
  if (mm_modem_get_current_modes(m, &allowed, &preferred)) {
    char *x = mm_modem_mode_build_string_from_mask(allowed),
         *y = mm_modem_mode_build_string_from_mask(preferred);
    s = g_strdup_printf("allowed: %s; preferred: %s", x, y);
    field(g, "current-modes", s);
    g_free(s);
    g_free(x);
    g_free(y);
  }
  MMModemModeCombination *modes = NULL;
  len = 0;
  a = cJSON_AddArrayToObject(g, "supported-modes");
  if (mm_modem_get_supported_modes(m, &modes, &len)) {
    for (guint i = 0; i < len; i++) {
      char *x = mm_modem_mode_build_string_from_mask(modes[i].allowed),
           *y = mm_modem_mode_build_string_from_mask(modes[i].preferred);
      s = g_strdup_printf("allowed: %s; preferred: %s", x, y);
      cJSON_AddItemToArray(a, cJSON_CreateString(s));
      g_free(s);
      g_free(x);
      g_free(y);
    }
    g_free(modes);
  }
  mask_strings(g, "supported-ip-families",
               mm_bearer_ip_family_build_string_from_mask(
                   mm_modem_get_supported_ip_families(m)));
  MMModemPortInfo *ports = NULL;
  len = 0;
  a = cJSON_AddArrayToObject(g, "ports");
  if (mm_modem_get_ports(m, &ports, &len)) {
    for (guint i = 0; i < len; i++) {
      s = g_strdup_printf("%s (%s)", ports[i].name,
                          mm_modem_port_type_get_string(ports[i].type));
      cJSON_AddItemToArray(a, cJSON_CreateString(s));
      g_free(s);
    }
    mm_modem_port_info_array_free(ports, len);
  }
  if (gsm) {
    cJSON *v = cJSON_AddObjectToObject(root, "3gpp");
    field(v, "imei", mm_modem_3gpp_get_imei(gsm));
    field(v, "operator-code", mm_modem_3gpp_get_operator_code(gsm));
    field(v, "operator-name", mm_modem_3gpp_get_operator_name(gsm));
    field(v, "registration-state",
          mm_modem_3gpp_registration_state_get_string(
              mm_modem_3gpp_get_registration_state(gsm)));
    field(v, "packet-service-state",
          mm_modem_3gpp_packet_service_state_get_string(
              mm_modem_3gpp_get_packet_service_state(gsm)));
    mask_strings(v, "enabled-locks",
                 mm_modem_3gpp_facility_build_string_from_mask(
                     mm_modem_3gpp_get_enabled_facility_locks(gsm)));
    field(v, "pco", "--");
    cJSON *eps = cJSON_AddObjectToObject(v, "eps");
    field(eps, "ue-mode-operation",
          mm_modem_3gpp_eps_ue_mode_operation_get_string(
              mm_modem_3gpp_get_eps_ue_mode_operation(gsm)));
    cJSON *initial = cJSON_AddObjectToObject(eps, "initial-bearer");
    field(initial, "dbus-path", mm_modem_3gpp_get_initial_eps_bearer_path(gsm));
    MMBearerProperties *properties =
        mm_modem_3gpp_get_initial_eps_bearer_settings(gsm);
    cJSON *settings = cJSON_AddObjectToObject(initial, "settings");
    field(settings, "apn",
          properties ? mm_bearer_properties_get_apn(properties) : NULL);
    field(settings, "user",
          properties ? mm_bearer_properties_get_user(properties) : NULL);
    char *family = properties
                       ? mm_bearer_ip_family_build_string_from_mask(
                             mm_bearer_properties_get_ip_type(properties))
                       : NULL;
    field(settings, "ip-type", family);
    g_free(family);
    g_clear_object(&properties);
    cJSON *nr = cJSON_AddObjectToObject(v, "5gnr"),
          *registration = cJSON_AddObjectToObject(nr, "registration-settings");
    field(registration, "drx-cycle", "--");
    field(registration, "mico-mode", "--");
  }
  cJSON *cdma = cJSON_AddObjectToObject(root, "cdma");
  const char *unused[] = {"activation-state",
                          "cdma1x-registration-state",
                          "esn",
                          "evdo-registration-state",
                          "meid",
                          "nid",
                          "sid",
                          NULL};
  for (int i = 0; unused[i]; i++)
    field(cdma, unused[i], "--");
  return out;
}
cJSON *state_bearer(MMBearer *b) {
  cJSON *out = cJSON_CreateObject(),
        *root = cJSON_AddObjectToObject(out, "bearer"),
        *status = cJSON_AddObjectToObject(root, "status"),
        *ipv4 = cJSON_AddObjectToObject(root, "ipv4-config");
  if (!b)
    return out;
  field(status, "interface", mm_bearer_get_interface(b));
  field(status, "connected", mm_bearer_get_connected(b) ? "yes" : "no");
  MMBearerProperties *properties=mm_bearer_get_properties(b);
  cJSON *settings=cJSON_AddObjectToObject(root,"properties");
  field(settings,"apn",properties ? mm_bearer_properties_get_apn(properties) : NULL);
  g_clear_object(&properties);
  MMBearerIpConfig *ip = mm_bearer_get_ipv4_config(b);
  if (ip) {
    field(ipv4, "method",
          mm_bearer_ip_method_get_string(mm_bearer_ip_config_get_method(ip)));
    field(ipv4, "address", mm_bearer_ip_config_get_address(ip));
    field(ipv4, "gateway", mm_bearer_ip_config_get_gateway(ip));
    char n[32];
    snprintf(n, sizeof(n), "%u", mm_bearer_ip_config_get_prefix(ip));
    field(ipv4, "prefix", n);
    snprintf(n, sizeof(n), "%u", mm_bearer_ip_config_get_mtu(ip));
    field(ipv4, "mtu", n);
    strings(ipv4, "dns", mm_bearer_ip_config_get_dns(ip));
    g_object_unref(ip);
  }
  return out;
}
static void runtime(cJSON *j, const char *key, const char *name) {
  char *s = ev_read(ev_run(), name);
  g_strstrip(s);
  cJSON_AddStringToObject(j, key, s);
  g_free(s);
}
static cJSON *ttl(void) {
  char *enabled = ev_conf("ttl.conf", "FIX_TTL_ENABLED", "yes"),
       *value = ev_conf("ttl.conf", "FIX_TTL_VALUE", "64"),
       *applied = ev_read(ev_run(), "ttl-applied"),
       *error = ev_read(ev_run(), "ttl-error");
  g_strstrip(applied);
  g_strstrip(error);
  char *key = g_strdup_printf("%s:%s", enabled, value);
  cJSON *j = cJSON_CreateObject();
  cJSON_AddBoolToObject(j, "enabled", !strcmp(enabled, "yes"));
  cJSON_AddNumberToObject(j, "value", atoi(value));
  cJSON_AddBoolToObject(j, "applied", !strcmp(applied, key) && !*error);
  cJSON_AddStringToObject(j, "error", error);
  g_free(enabled);
  g_free(value);
  g_free(applied);
  g_free(error);
  g_free(key);
  return j;
}
static cJSON *imei(MMModem3gpp *gsm, gboolean config) {
  cJSON *d = ev_json("/usr/share/microstick", "default-imei.json"),
        *v = ev_json(ev_etc(), "imei.json"), *j = cJSON_CreateObject();
  const char *def = ev_str(d, "imei"), *custom = ev_str(v, "imei");
  cJSON_AddStringToObject(j, "configured", custom);
  cJSON_AddStringToObject(j, "default", def);
  cJSON_AddStringToObject(j, "effective", *custom ? custom : def);
  cJSON_AddStringToObject(j, "source",
                          *custom ? "custom"
                          : *def  ? "default"
                                  : "device");
  runtime(j, "error", "imei-error");
  if (!config)
    cJSON_AddStringToObject(j, "current",
                            gsm ? mm_modem_3gpp_get_imei(gsm) : "");
  cJSON_Delete(d);
  cJSON_Delete(v);
  return j;
}
static cJSON *lte_profile(gboolean config) {
  cJSON *saved=ev_json(ev_etc(),"lte.json"), *selected=ev_json(ev_run(),"lte-profile.json");
  char *legacy=ev_conf("lte.properties","apn","");
  char *comma=strchr(legacy,','); if (comma) *comma=0;
  const char *mode=ev_str(saved,"profile"), *manual=ev_str(saved,"apn");
  if (!*mode) mode=*manual || *legacy ? "manual" : "auto";
  if (!*manual) manual=legacy;
  gboolean valid=selected && !strcmp(ev_str(selected,"profile"),mode);
  cJSON *active=ev_json(ev_run(),"bearer-control.json");
  const cJSON *bearer=cJSON_GetObjectItemCaseSensitive(active,"bearer");
  const char *actual=ev_str(cJSON_GetObjectItemCaseSensitive(bearer,"properties"),"apn");
  if (strcmp(ev_str(cJSON_GetObjectItemCaseSensitive(bearer,"status"),"connected"),"yes")) actual="";
  cJSON *out=cJSON_CreateObject();
  cJSON_AddStringToObject(out,"profile",mode);
  cJSON_AddStringToObject(out,"effective_apn",!strcmp(mode,"manual") ? manual : valid ? ev_str(selected,"effective_apn") : actual);
  cJSON_AddStringToObject(out,"provider",valid ? ev_str(selected,"provider") : "");
  cJSON_AddStringToObject(out,"profile_id",valid ? ev_str(selected,"profile_id") : "");
  cJSON_AddStringToObject(out,"state",!strcmp(mode,"manual") ? "ready" : valid ? ev_str(selected,"state") : *actual ? "ready" : "pending");
  if (config) {
    cJSON_AddStringToObject(out,"auth",ev_str(saved,"auth"));
    cJSON *catalog=apn_catalog();
    cJSON_AddItemToObject(out,"profiles",apn_profile_list(catalog));
    cJSON_Delete(catalog);
  }
  cJSON_Delete(saved); cJSON_Delete(selected); cJSON_Delete(active); g_free(legacy);
  return out;
}
cJSON *state_status(MMModem *m, MMModem3gpp *gsm, gboolean config) {
  cJSON *j = cJSON_CreateObject();
  cJSON_AddItemToObject(j, "ttl", ttl());
  cJSON_AddItemToObject(j, "imei", imei(gsm, config));
  char *policy = ev_conf("network.conf", "UPLINK_MODE", "auto");
  cJSON_AddStringToObject(j, "policy", policy);
  g_free(policy);
  cJSON_AddItemToObject(j,"device",discovery_status());
  if (config) {
    char *ssid = ev_conf("hostapd.conf", "ssid", ""),
         *hex = ev_conf("hostapd.conf", "ssid2", "");
    if (!*ssid && strlen(hex) % 2 == 0 && strlen(hex) <= 64) {
      g_free(ssid);
      ssid = g_malloc0(strlen(hex) / 2 + 1);
      for (size_t i = 0; i < strlen(hex) / 2; i++) {
        char b[3] = {hex[i * 2], hex[i * 2 + 1], 0};
        ssid[i] = (char)strtoul(b, NULL, 16);
      }
    }
    cJSON_AddStringToObject(j, "ssid", ssid);
    g_free(ssid);
    g_free(hex);
    cJSON *lte = ev_json(ev_etc(), "lte.json");
    char *apn = ev_conf("lte.properties", "apn", "");
    char *comma = strchr(apn, ',');
    if (comma)
      *comma = 0;
    cJSON_AddStringToObject(j, "apn", lte ? ev_str(lte, "apn") : apn);
    g_free(apn);
    cJSON_Delete(lte);
    cJSON_AddItemToObject(j,"lte",lte_profile(TRUE));
    return j;
  }
  cJSON_AddNumberToObject(j, "api_version", 1);
  char *mode = wifi_mode(),
       *enabled = ev_conf("network.conf", "LTE_ENABLED", "no");
  cJSON_AddStringToObject(j, "wifi", mode);
  cJSON_AddBoolToObject(j, "lte_enabled", !strcmp(enabled, "yes"));
  g_free(mode);
  g_free(enabled);
  cJSON *lte=lte_profile(FALSE);
  cJSON_AddItemToObject(j,"lte",lte);
  runtime(lte, "error", "lte-error");
  char *p = ev_path(ev_run(), "lte-connecting");
  cJSON_AddBoolToObject(lte, "connecting", g_file_test(p, G_FILE_TEST_EXISTS));
  g_free(p);
  char *s = ev_read(ev_run(), "wifi-status"), **lines = g_strsplit(s, "\n", -1);
  const char *ssid = "", *state = "";
  for (int i = 0; lines[i]; i++) {
    if (g_str_has_prefix(lines[i], "ssid="))
      ssid = lines[i] + 5;
    if (g_str_has_prefix(lines[i], "wpa_state="))
      state = lines[i] + 10;
  }
  char ssid_hex[65]; char *decoded=ssid_display(ssid,FALSE,ssid_hex);
  cJSON_AddStringToObject(j, "ssid", decoded);g_free(decoded);
  cJSON_AddStringToObject(j, "wifi_state", state);
  cJSON_AddItemToObject(j,"wifi_connection",wifi_guard_status());
  g_strfreev(lines);
  g_free(s);
  runtime(j, "error", "wifi-error");
  runtime(j, "message", "error");
  runtime(j, "uplink", "uplink");
  cJSON *lan = ev_lan();
  const char *keys[] = {"usb", "ap"};
  for (int i = 0; i < 2; i++) {
    s = g_strdup(
        ev_str(cJSON_GetObjectItemCaseSensitive(lan, keys[i]), "address"));
    char *slash = strchr(s, '/');
    if (slash)
      *slash = 0;
    cJSON_AddStringToObject(j, i ? "ap_address" : "usb_address", s);
    g_free(s);
  }
  cJSON_Delete(lan);
  cJSON_AddItemToObject(j, "modem", state_modem(m, gsm));
  cJSON_AddItemToObject(j, "addresses", ev_addresses());
  cJSON *scan = ev_json(ev_run(), "scan.json");
  cJSON_AddItemToObject(j, "networks",
                        cJSON_IsArray(scan) ? scan : cJSON_CreateArray());
  if (scan && !cJSON_IsArray(scan))
    cJSON_Delete(scan);
  return j;
}
struct ttl_check {
  unsigned rules, value;
  gboolean valid;
};
static int ttl_rule(const struct nlmsghdr *h, void *data) {
  struct nftnl_rule *r = nftnl_rule_alloc();
  if (nftnl_rule_nlmsg_parse(h, r) == 0) {
    const char *t = nftnl_rule_get_str(r, NFTNL_RULE_TABLE);
    if (t && !strcmp(t, "microstick_ttl")) {
      if (getenv("MICROSTICK_DEBUG_TTL"))
        nftnl_rule_fprintf(stderr, r, NFTNL_OUTPUT_DEFAULT, 0);
      struct ttl_check *c = data;
      c->rules++;
      struct nftnl_expr_iter *it = nftnl_expr_iter_create(r);
      struct nftnl_expr *e;
      while (it && (e = nftnl_expr_iter_next(it))) {
        const char *name = nftnl_expr_get_str(e, NFTNL_EXPR_NAME);
        if (name && !strcmp(name, "immediate")) {
          uint32_t len = 0;
          const unsigned char *v = nftnl_expr_get(e, NFTNL_EXPR_IMM_DATA, &len);
          if (v && len >= 1 && len <= 4 && v[0] == c->value)
            c->valid = TRUE;
        }
        if (name && !strcmp(name, "bitwise")) {
          uint32_t len = 0, masklen = 0;
          const unsigned char *v = nftnl_expr_get(e, NFTNL_EXPR_BITWISE_XOR,
                                                  &len),
                              *mask = nftnl_expr_get(e, NFTNL_EXPR_BITWISE_MASK,
                                                     &masklen);
          /* nft writes TTL with its adjacent protocol byte to update checksum.
           */
          if (v && mask && len >= 1 && len <= 4 && masklen == len &&
              mask[0] == 0 && v[0] == c->value)
            c->valid = TRUE;
        }
      }
      if (it)
        nftnl_expr_iter_destroy(it);
    }
  }
  nftnl_rule_free(r);
  return MNL_CB_OK;
}
gboolean state_ttl_matches(void) {
  char *enabled = ev_conf("ttl.conf", "FIX_TTL_ENABLED", "yes"),
       *value = ev_conf("ttl.conf", "FIX_TTL_VALUE", "64"),
       *applied = ev_read(ev_run(), "ttl-applied");
  g_strstrip(applied);
  char *key =
      g_strdup_printf("%s:%u", enabled, (unsigned)strtoul(value, NULL, 10));
  gboolean ok = !strcmp(applied, key);
  g_free(key);
  g_free(enabled);
  g_free(value);
  g_free(applied);
  return ok;
}
static int ttl_table(const struct nlmsghdr *h, void *data) {
  (void)h;
  *(gboolean *)data = TRUE;
  return MNL_CB_OK;
}
gboolean state_ttl_healthy(void) {
  /* A native read-only dump, never a periodic nft subprocess. */
  struct mnl_socket *nl = mnl_socket_open(NETLINK_NETFILTER);
  if (!nl)
    return FALSE;
  if (mnl_socket_bind(nl, 0, MNL_SOCKET_AUTOPID) < 0) {
    mnl_socket_close(nl);
    return FALSE;
  }
  struct timeval tv = {.tv_sec = 0, .tv_usec = 100000};
  setsockopt(mnl_socket_get_fd(nl), SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
  char b[MNL_SOCKET_BUFFER_SIZE];
  unsigned seq = 1;
  char *value = ev_conf("ttl.conf", "FIX_TTL_VALUE", "64");
  struct ttl_check check = {.value = (unsigned)strtoul(value, NULL, 10)};
  g_free(value);
  struct nlmsghdr *h = nftnl_rule_nlmsg_build_hdr(
      b, NFT_MSG_GETRULE, NFPROTO_IPV4, NLM_F_DUMP, seq);
  gboolean ok = mnl_socket_sendto(nl, h, h->nlmsg_len) >= 0;
  int n, rc = 1;
  while (ok && rc > 0 && (n = mnl_socket_recvfrom(nl, b, sizeof(b))) > 0)
    rc = mnl_cb_run(b, n, seq, mnl_socket_get_portid(nl), ttl_rule, &check);
  char *enabled = ev_conf("ttl.conf", "FIX_TTL_ENABLED", "yes");
  gboolean yes = !strcmp(enabled, "yes");
  g_free(enabled);
  gboolean table = FALSE;
  struct nftnl_table *t = nftnl_table_alloc();
  nftnl_table_set_str(t, NFTNL_TABLE_NAME, "microstick_ttl");
  h = nftnl_table_nlmsg_build_hdr(b, NFT_MSG_GETTABLE, NFPROTO_IPV4, NLM_F_ACK,
                                  ++seq);
  nftnl_table_nlmsg_build_payload(h, t);
  nftnl_table_free(t);
  if (ok && rc == 0 && mnl_socket_sendto(nl, h, h->nlmsg_len) >= 0) {
    rc = 1;
    while (rc > 0 && (n = mnl_socket_recvfrom(nl, b, sizeof(b))) > 0)
      rc = mnl_cb_run(b, n, seq, mnl_socket_get_portid(nl), ttl_table, &table);
  }
  mnl_socket_close(nl);
  if (getenv("MICROSTICK_DEBUG_TTL"))
    fprintf(stderr,
            "TTL check ok=%d rc=%d table=%d rules=%u valid=%d expected=%u "
            "enabled=%d\n",
            ok, rc, table, check.rules, check.valid, check.value, yes);
  return ok && rc == 0 && table &&
         (yes ? (check.rules == 1 && check.valid) : check.rules == 0);
}
