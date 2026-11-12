#include "state.h"
#include "wifi-guard.h"
#include "discovery.h"
#include <errno.h>
#include <fcntl.h>
#include <net/if.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

static GMainLoop *loop;
static MMManager *manager;
static MMModem *modem;
static MMModem3gpp *gsm;
static MMModemSimple *simple;
static MMBearer *bearer;
static gboolean discovering, bearers_loading, disconnecting;
static guint scheduled, retry_timer, lost_timer;
static GPid wifi_worker, lte_worker, ttl_worker;
static char *wifi_applied, *wifi_running, *last_bearer_paths;
static cJSON *lte_applied, *lte_working;
static int delay = 5;
static double next_lte;
static gboolean force_lte;
static unsigned wifi_applications, lte_applications, ttl_applications;
static double ttl_retry_after;
static gboolean observe_only;
static unsigned sim_revision;
static double wifi_retry_after;
static unsigned wifi_delay = 5;
static void changed(unsigned type, gpointer data);
static void load_bearers(void);
static const char *helper(void) {
  const char *s = getenv("MICROSTICK_NETWORK_HELPER");
  return s ? s : "/usr/libexec/microstick-network";
}
static void conf_set(const char *key, const char *value) {
  int lock=ev_config_lock();if(lock<0){ev_write(ev_run(),"error","config_lock_failed");return;}
  char *s = ev_read(ev_etc(), "network.conf"),
       **lines = g_strsplit(s, "\n", -1);
  GString *out = g_string_new("");
  gboolean found = FALSE;
  size_t n = strlen(key);
  for (int i = 0; lines[i]; i++) {
    if (!*lines[i] && !lines[i + 1])
      break;
    if (!strncmp(lines[i], key, n) && lines[i][n] == '=') {
      g_string_append_printf(out, "%s=%s\n", key, value);
      found = TRUE;
    } else
      g_string_append_printf(out, "%s\n", lines[i]);
  }
  if (!found)
    g_string_append_printf(out, "%s=%s\n", key, value);
  ev_write(ev_etc(), "network.conf", out->str);
  g_string_free(out, TRUE);
  g_strfreev(lines);
  g_free(s);
  ev_config_unlock(lock);
}
static void discover_wifi(void) {
  char *wifi = ev_read(ev_run(), "wifi-interface");
  g_strstrip(wifi);
  if (!ev_valid_iface(wifi) || !if_nametoindex(wifi)) {
    GDir *d = g_dir_open("/sys/class/net", 0, NULL);
    const char *name;
    gboolean found = FALSE;
    if (d) {
      while ((name = g_dir_read_name(d))) {
        char *p = g_strdup_printf("/sys/class/net/%s/phy80211", name);
        gboolean yes = g_file_test(p, G_FILE_TEST_IS_DIR);
        g_free(p);
        if (yes) {
          ev_write(ev_run(), "wifi-interface", name);
          found = TRUE;
          break;
        }
      }
      g_dir_close(d);
    }
    if (!found)
      ev_write(ev_run(), "wifi-interface", "unavailable");
  }
  g_free(wifi);
}
static void notify(GObject *object, GParamSpec *spec, gpointer data) {
  (void)object;
  (void)spec;
  (void)data;
  changed(1, NULL);
}
static void bearer_list(GObject *object, GAsyncResult *result, gpointer data) {
  MMModem *requested = data;
  GError *error = NULL;
  GList *list = mm_modem_list_bearers_finish(MM_MODEM(object), result, &error);
  bearers_loading = FALSE;
  if (error)
    g_clear_pointer(&last_bearer_paths, g_free);
  if (modem == requested) {
    g_clear_object(&bearer);
    for (GList *p = list; p; p = p->next) {
      MMBearer *b = p->data;
      if (!bearer || mm_bearer_get_connected(b)) {
        g_set_object(&bearer, b);
        if (mm_bearer_get_connected(b))
          break;
      }
    }
    if (bearer)
      g_signal_connect(bearer, "notify", G_CALLBACK(notify), NULL);
    changed(1, NULL);
  }
  g_list_free_full(list, g_object_unref);
  g_clear_error(&error);
  g_object_unref(requested);
}
static void load_bearers(void) {
  if (!modem || bearers_loading)
    return;
  const char *const *paths = mm_modem_get_bearer_paths(modem);
  char *joined = g_strjoinv(",", (char **)paths);
  if (last_bearer_paths && !strcmp(last_bearer_paths, joined ? joined : "") &&
      (bearer || !paths || !paths[0])) {
    g_free(joined);
    return;
  }
  g_free(last_bearer_paths);
  last_bearer_paths = g_strdup(joined ? joined : "");
  g_free(joined);
  bearers_loading = TRUE;
  mm_modem_list_bearers(modem, NULL, bearer_list, g_object_ref(modem));
}
static void select_modem(void) {
  GList *objects =
      manager
          ? g_dbus_object_manager_get_objects(G_DBUS_OBJECT_MANAGER(manager))
          : NULL;
  MMModem *m = NULL;
  MMModem3gpp *g = NULL;
  MMModemSimple *s = NULL;
  for (GList *p = objects; p; p = p->next) {
    MMModem *candidate = mm_object_get_modem(MM_OBJECT(p->data));
    if (candidate) {
      m = candidate;
      g = mm_object_get_modem_3gpp(MM_OBJECT(p->data));
      s = mm_object_get_modem_simple(MM_OBJECT(p->data));
      break;
    }
  }
  if (m != modem) {
    ev_remove(ev_run(),"lte-profile.json");
    sim_revision++;
    g_clear_object(&modem);
    g_clear_object(&gsm);
    g_clear_object(&simple);
    g_clear_object(&bearer);
    g_clear_pointer(&last_bearer_paths, g_free);
    modem = m;
    gsm = g;
    simple = s;
    if (modem) {
      g_signal_connect(modem, "notify", G_CALLBACK(notify), NULL);
      if (gsm)
        g_signal_connect(gsm, "notify", G_CALLBACK(notify), NULL);
    }
  } else {
    g_clear_object(&m);
    g_clear_object(&g);
    g_clear_object(&s);
  }
  g_list_free_full(objects, g_object_unref);
  load_bearers();
  changed(1, NULL);
}
static void objects_changed(GDBusObjectManager *m, GDBusObject *object,
                            gpointer data) {
  (void)m;
  (void)object;
  (void)data;
  select_modem();
}
static void owner_changed(GObject *object, GParamSpec *spec, gpointer data) {
  (void)object;
  (void)spec;
  (void)data;
  select_modem();
}
static void manager_ready(GObject *object, GAsyncResult *result,
                          gpointer data) {
  (void)object;
  (void)data;
  GError *error = NULL;
  manager = mm_manager_new_finish(result, &error);
  discovering = FALSE;
  if (manager) {
    g_signal_connect(manager, "object-added", G_CALLBACK(objects_changed),
                     NULL);
    g_signal_connect(manager, "object-removed", G_CALLBACK(objects_changed),
                     NULL);
    g_signal_connect(manager, "notify::name-owner", G_CALLBACK(owner_changed),
                     NULL);
    select_modem();
  }
  g_clear_error(&error);
}
static void sim_changed(GDBusConnection *bus, const gchar *sender,
                        const gchar *path, const gchar *interface,
                        const gchar *signal, GVariant *parameters, gpointer data) {
  (void)bus; (void)sender; (void)path; (void)interface;
  (void)signal; (void)parameters; (void)data;
  sim_revision++;
  ev_remove(ev_run(),"lte-profile.json");
  force_lte=TRUE;
  changed(1,NULL);
}
static void bus_ready(GObject *object, GAsyncResult *result, gpointer data) {
  (void)object;
  (void)data;
  GError *error = NULL;
  GDBusConnection *bus = g_bus_get_finish(result, &error);
  if (bus) {
    g_dbus_connection_signal_subscribe(bus,"org.freedesktop.ModemManager1",
        "org.freedesktop.DBus.Properties","PropertiesChanged",NULL,
        "org.freedesktop.ModemManager1.Sim",G_DBUS_SIGNAL_FLAGS_NONE,
        sim_changed,NULL,NULL);
    mm_manager_new(bus, G_DBUS_OBJECT_MANAGER_CLIENT_FLAGS_DO_NOT_AUTO_START,
                   NULL, manager_ready, NULL);
    g_object_unref(bus);
  } else
    discovering = FALSE;
  g_clear_error(&error);
}
static void discover_modem(void) {
  if (!manager && !discovering) {
    discovering = TRUE;
    g_bus_get(G_BUS_TYPE_SYSTEM, NULL, bus_ready, NULL);
  }
}
static void disconnected(GObject *object, GAsyncResult *result, gpointer data) {
  (void)data;
  GError *error = NULL;
  mm_modem_simple_disconnect_finish(MM_MODEM_SIMPLE(object), result, &error);
  g_clear_error(&error);
  disconnecting = FALSE;
  changed(1, NULL);
}
static gboolean retry(gpointer data) {
  (void)data;
  retry_timer = 0;
  force_lte = TRUE;
  changed(1, NULL);
  return G_SOURCE_REMOVE;
}
static void child_done(GPid pid, gint status, gpointer data) {
  const char *kind = data;
  g_spawn_close_pid(pid);
  if (!strcmp(kind, "wifi")) {
    wifi_worker = 0;
    if (!status) {
      g_free(wifi_applied);
      wifi_applied = g_strdup(wifi_running);
      wifi_retry_after = 0;
      wifi_delay = 5;
    } else {
      ev_write(ev_run(), "error", "wifi_apply_failed");
      wifi_retry_after = ev_now() + wifi_delay;
      g_timeout_add_seconds(wifi_delay, retry, NULL);
      wifi_delay = MIN(60, wifi_delay * 2);
    }
  } else if (!strcmp(kind, "ttl")) {
    ttl_worker = 0;
    ttl_retry_after = status ? ev_now() + 20 : 0;
  } else {
    lte_worker = 0;
    cJSON_Delete(lte_applied);
    lte_applied = lte_working;
    lte_working = NULL;
    if (status) {
      next_lte = ev_now() + delay;
      if (retry_timer)
        g_source_remove(retry_timer);
      retry_timer = g_timeout_add_seconds(delay, retry, NULL);
      delay = MIN(60, delay * 2);
    } else {
      delay = 5;
      next_lte = 0;
    }
  }
  changed(1, NULL);
}
static GPid spawn(const char *verb, const char *value) {
  GPid p = 0;
  char **env = g_get_environ();
  env = g_environ_setenv(env, "MICROSTICK_CACHED_MODEM", "yes", TRUE);
  if (!strcmp(verb, "ttl"))
    env = g_environ_setenv(env, "MICROSTICK_TTL_FORCE", "yes", TRUE);
  char *argv[] = {(char *)helper(), (char *)verb, (char *)(value ? value : ""),
                  NULL};
  gboolean ok = g_spawn_async(NULL, argv, env, G_SPAWN_DO_NOT_REAP_CHILD, NULL,
                              NULL, &p, NULL);
  g_strfreev(env);
  if (ok)
    g_child_watch_add(p, child_done, (gpointer)verb);
  else
    ev_write(ev_run(), "error", "spawn_failed");
  return ok ? p : 0;
}
static gboolean lost(gpointer data) {
  (void)data;
  lost_timer = 0;
  char *mode = wifi_mode();
  if (!strcmp(mode, "sta")) {
    conf_set("WIFI_MODE", "ap");
    ev_write(ev_run(), "wifi-error", "connection_lost");
  }
  g_free(mode);
  changed(1, NULL);
  return G_SOURCE_REMOVE;
}
static void update_dns(void) {
  char *up = ev_uplink(), *wifi = ev_read(ev_run(), "wifi-interface");
  g_strstrip(wifi);
  ev_write(ev_run(), "uplink", up);
  char *dns = ev_read(ev_run(), !strcmp(up, wifi) ? "wifi-dns" : "lte-dns"),
       *old = ev_read(ev_run(), "upstream-resolv.conf");
  if (strcmp(dns, old)) {
    ev_write(ev_run(), "upstream-resolv.conf", dns);
    char *s = ev_read(ev_run(), "dnsmasq.pid");
    long pid = strtol(s, NULL, 10);
    if (pid > 1)
      kill((pid_t)pid, SIGHUP);
    g_free(s);
  }
  g_free(old);
  g_free(dns);
  g_free(wifi);
  g_free(up);
}
static void wifi_status(const char *mode, const char *wifi) {
  ev_watch_wifi("/run/wpa_supplicant", wifi, changed, NULL);
  ev_watch_wifi("/run/hostapd", wifi, changed, NULL);
  char *status = !strcmp(mode, "sta")
                     ? ev_ctrl("/run/wpa_supplicant", wifi, "STATUS")
                     : g_strdup("");
  ev_write(ev_run(), "wifi-status", status);
  gboolean connected = !wifi_worker && wifi_ready(wifi,status);
  wifi_guard_tick(mode,wifi,connected);
  /* A deliberate new connection has its own 45s action deadline. */
  char *state = ev_read(ev_run(), "wifi-state");
  g_strstrip(state);
  gboolean connecting = !strcmp(state, "connecting");
  if (!strcmp(mode, "sta") && !connected && !wifi_worker && !connecting) {
    if (!lost_timer)
      lost_timer = g_timeout_add_seconds(30, lost, NULL);
  } else if (lost_timer) {
    g_source_remove(lost_timer);
    lost_timer = 0;
  }
  if (connected) {
    /* Only validation/commit clears a previous safety error. */
    if (connecting)
      ev_write(ev_run(), "wifi-state", "connected");
  }
  g_free(state);
  g_free(status);
}
static void syswrite(const char *base, const char *name, const char *value) {
  char *p = ev_path(base, name);
  int fd = open(p, O_WRONLY | O_CLOEXEC);
  if (fd >= 0) {
    ssize_t n = write(fd, value, strlen(value));
    (void)n;
    close(fd);
  }
  g_free(p);
}
static void indicator(const char *mode, const char *enabled) {
  const char *state = !strcmp(mode, "sta") ? "connecting" : "waiting";
  char *up = ev_read(ev_run(), "uplink"),
       *wifierror = ev_read(ev_run(), "wifi-error"),
       *lteerror = ev_read(ev_run(), "lte-error");
  g_strstrip(up);
  if (*up)
    state = "online";
  if (*wifierror || (!strcmp(enabled, "yes") && *lteerror && !*up))
    state = "error";
  if (modem && (mm_modem_get_state(modem) == MM_MODEM_STATE_LOCKED ||
                (mm_modem_get_state(modem) == MM_MODEM_STATE_FAILED &&
                 mm_modem_get_state_failed_reason(modem) !=
                     MM_MODEM_STATE_FAILED_REASON_SIM_MISSING)))
    state = "error";
  char *old = ev_read(ev_run(), "led-state");
  g_strstrip(old);
  if (strcmp(old, state)) {
    GDir *d = g_dir_open("/sys/class/leds", 0, NULL);
    const char *name;
    while (d && (name = g_dir_read_name(d))) {
      const char *suffix = strrchr(name, ':');
      if (!suffix)
        continue;
      suffix++;
      gboolean generic =
                   !strcmp(suffix, "status") || !strcmp(suffix, "indicator"),
               on = generic ||
                    (!strcmp(state, "waiting") &&
                     (!strcmp(suffix, "wifi") || !strcmp(suffix, "wlan"))) ||
                    (!strcmp(state, "connecting") &&
                     (!strcmp(suffix, "wifi") || !strcmp(suffix, "wlan"))) ||
                    (!strcmp(state, "online") && !strcmp(suffix, "internet")) ||
                    (!strcmp(state, "error") && !strcmp(suffix, "os"));
      if (!generic && strcmp(suffix, "wifi") && strcmp(suffix, "wlan") &&
          strcmp(suffix, "internet") && strcmp(suffix, "os"))
        continue;
      char *base = g_strdup_printf("/sys/class/leds/%s", name),
           *tr = ev_read(base, "trigger");
      if (!on) {
        syswrite(base, "trigger", "none");
        syswrite(base, "brightness", "0");
      } else if (!strcmp(state, "error") && strstr(tr, "heartbeat"))
        syswrite(base, "trigger", "heartbeat");
      else if (strstr(tr, "timer")) {
        syswrite(base, "trigger", "timer");
        syswrite(base, "delay_on",
                 !strcmp(state, "online")  ? "80"
                 : !strcmp(state, "error") ? "100"
                                           : "150");
        syswrite(base, "delay_off",
                 !strcmp(state, "online")       ? "4920"
                 : !strcmp(state, "error")      ? "700"
                 : !strcmp(state, "connecting") ? "150"
                                                : "1850");
      }
      g_free(tr);
      g_free(base);
    }
    if (d)
      g_dir_close(d);
    ev_write(ev_run(), "led-state", state);
  }
  g_free(old);
  g_free(up);
  g_free(wifierror);
  g_free(lteerror);
}
static cJSON *control(const char *enabled) {
  cJSON *j = cJSON_CreateObject();
  cJSON_AddNumberToObject(j,"sim_revision",sim_revision);
  cJSON_AddStringToObject(j, "enabled", enabled);
  if (modem) {
    cJSON_AddStringToObject(j,"sim",mm_modem_get_sim_path(modem) ? mm_modem_get_sim_path(modem) : "");
    cJSON_AddNumberToObject(j, "state", mm_modem_get_state(modem));
    cJSON_AddNumberToObject(j, "reason",
                            mm_modem_get_state_failed_reason(modem));
    cJSON_AddNumberToObject(j, "unlock", mm_modem_get_unlock_required(modem));
    if (gsm)
      cJSON_AddStringToObject(j, "operator",
                              mm_modem_3gpp_get_operator_code(gsm));
  }
  if (bearer)
    cJSON_AddItemToObject(j, "bearer", state_bearer(bearer));
  const char *files[] = {"lte.json", "lte.properties", "lan.json", NULL};
  for (int i = 0; files[i]; i++) {
    char *s = ev_read(ev_etc(), files[i]);
    cJSON_AddStringToObject(j, files[i], s);
    g_free(s);
  }
  return j;
}
static void blocked(const char *error) {
  ev_write(ev_run(), "lte-error", error);
  ev_write(ev_run(), "error", error);
  char *active = ev_read(ev_run(), "lte-iface");
  if (*active && !lte_worker) {
    cJSON_Delete(lte_working);
    lte_working = NULL;
    lte_worker = spawn("clear", NULL);
  }
  g_free(active);
}
static gboolean check(gpointer data) {
  (void)data;
  scheduled = 0;
  discover_wifi();
  discover_modem();
  load_bearers();
  if (observe_only)
    return G_SOURCE_REMOVE;
  discovery_tick();
  if (!ttl_worker && ev_now() >= ttl_retry_after && !state_ttl_matches()) {
    ttl_worker = spawn("ttl", NULL);
    ttl_applications++;
  }
  char *policy = ev_conf("network.conf", "UPLINK_MODE", "auto"),
       *init = ev_path(ev_run(), "auto-initialized");
  if (!strcmp(policy, "auto") && !g_file_test(init, G_FILE_TEST_EXISTS) &&
      modem) {
    gboolean missing = mm_modem_get_state_failed_reason(modem) ==
                       MM_MODEM_STATE_FAILED_REASON_SIM_MISSING;
    conf_set("LTE_ENABLED", missing ? "no" : "yes");
    char *saved = ev_read(ev_etc(), "wpa_supplicant.conf");
    conf_set("WIFI_MODE", missing && strstr(saved, "network={") ? "sta" : "ap");
    g_free(saved);
    ev_remove(ev_run(), "lte-request");
    ev_write(ev_run(), "auto-initialized", "yes");
  }
  g_free(init);
  g_free(policy);
  char *mode = wifi_mode(),
       *enabled = ev_conf("network.conf", "LTE_ENABLED", "no"),
       *wifi = ev_read(ev_run(), "wifi-interface");
  g_strstrip(wifi);
  char *scan = ev_path(ev_run(), "scan-active");
  gboolean scanning = g_file_test(scan, G_FILE_TEST_EXISTS);
  g_free(scan);
  if (strcmp(mode, "ap") && strcmp(mode, "sta") && strcmp(mode, "off")) {
    ev_write(ev_run(), "error", "invalid_wifi_mode");
    goto done;
  }
  char *config = !strcmp(mode,"ap") ? ev_read(ev_etc(),"hostapd.conf") : wifi_config();
  char *desired = g_strdup_printf("%s:%s:%s", mode, wifi, config);
  g_free(config);
  gboolean alive = !strcmp(mode, "ap")    ? ev_alive("hostapd.pid")
                   : !strcmp(mode, "sta") ? ev_alive("wpa_supplicant.pid")
                                          : TRUE;
  if (!strcmp(mode, "sta")) {
    char *pid = g_strdup_printf("dhcp-%s.pid", wifi);
    alive = alive && ev_alive(pid);
    g_free(pid);
  }
  if (!scanning && !wifi_worker && ev_now() >= wifi_retry_after &&
      (!wifi_applied || strcmp(wifi_applied, desired) || !alive) &&
      (!strcmp(mode, "off") || if_nametoindex(wifi))) {
    g_free(wifi_running);
    wifi_running = g_strdup(desired);
    wifi_worker = spawn("wifi", mode);
    wifi_applications++;
  }
  g_free(desired);
  if (!scanning || !strcmp(mode,"sta"))
    wifi_status(mode, wifi);
  update_dns();
  if (!strcmp(enabled, "no") && modem &&
      mm_modem_get_state(modem) == MM_MODEM_STATE_CONNECTED && !disconnecting) {
    if (simple) {
      disconnecting = TRUE;
      mm_modem_simple_disconnect(simple, NULL, NULL, disconnected, NULL);
    }
  }
  if (!lte_worker && !bearers_loading) {
    const char *error = NULL;
    if (!strcmp(enabled, "yes")) {
      if (!modem)
        error = "modem_unavailable";
      else if (mm_modem_get_state_failed_reason(modem) ==
               MM_MODEM_STATE_FAILED_REASON_SIM_MISSING)
        error = "sim_missing";
      else {
        MMModemLock lock = mm_modem_get_unlock_required(modem);
        if (lock == MM_MODEM_LOCK_SIM_PIN)
          error = "sim_pin_required";
        else if (lock == MM_MODEM_LOCK_SIM_PUK)
          error = "sim_puk_required";
        else if (lock != MM_MODEM_LOCK_NONE && lock != MM_MODEM_LOCK_SIM_PIN2 &&
                 lock != MM_MODEM_LOCK_SIM_PUK2 &&
                 lock != MM_MODEM_LOCK_UNKNOWN)
          error = "sim_locked";
      }
    }
    if (error)
      blocked(error);
    else {
      cJSON *current = control(enabled);
      char *rp = ev_path(ev_run(), "lte-retry");
      gboolean manual = g_file_test(rp, G_FILE_TEST_EXISTS);
      g_free(rp);
      if ((manual || force_lte || !lte_applied ||
           !cJSON_Compare(current, lte_applied, TRUE)) &&
          (manual || ev_now() >= next_lte)) {
        cJSON *m = state_modem(modem, gsm), *b = state_bearer(bearer);
        ev_write_json(ev_run(), "modem-control.json", m);
        ev_write_json(ev_run(), "bearer-control.json", b);
        cJSON_Delete(m);
        cJSON_Delete(b);
        ev_remove(ev_run(), "lte-retry");
        force_lte = FALSE;
        cJSON_Delete(lte_working);
        lte_working = cJSON_Duplicate(current, 1);
        lte_worker = spawn("lte", NULL);
        lte_applications++;
      }
      cJSON_Delete(current);
    }
  }
  indicator(mode, enabled);
done:
  g_free(mode);
  g_free(enabled);
  g_free(wifi);
  return G_SOURCE_REMOVE;
}
static void changed(unsigned type, gpointer data) {
  (void)type;
  (void)data;
  if (!scheduled)
    scheduled = g_timeout_add(100, check, NULL);
}
static gboolean watchdog(gpointer data) {
  (void)data;
  if (observe_only) {
    changed(0, NULL);
    return G_SOURCE_CONTINUE;
  }
  if (!ttl_worker && ev_now() >= ttl_retry_after &&
      (!state_ttl_matches() || !state_ttl_healthy())) {
    ttl_worker = spawn("ttl", NULL);
    ttl_applications++;
  }
  if (bearer && mm_bearer_get_connected(bearer)) {
    MMBearerIpConfig *ip = mm_bearer_get_ipv4_config(bearer);
    if (ip &&
        mm_bearer_ip_config_get_method(ip) == MM_BEARER_IP_METHOD_STATIC) {
      char *cidr = g_strdup_printf("%s/%u", mm_bearer_ip_config_get_address(ip),
                                   mm_bearer_ip_config_get_prefix(ip));
      char *route = ev_uplink();
      if (!ev_address(mm_bearer_get_interface(bearer), cidr) || !*route) {
        force_lte = TRUE;
        ev_remove(ev_run(), "lte-applied");
      }
      g_free(cidr);
      g_free(route);
    }
    g_clear_object(&ip);
  }
  changed(0, NULL);
  return G_SOURCE_CONTINUE;
}
static cJSON *handle(cJSON *q, int fd, gpointer data) {
  (void)fd;
  (void)data;
  const char *m = ev_str(q, "method");
  if (!strcmp(m,"wifi_confirm")) return wifi_confirm(q);
  if (!strcmp(m, "status") || !strcmp(m, "config"))
    return state_status(modem, gsm, !strcmp(m, "config"));
  if (!strcmp(m, "diagnostics")) {
    cJSON *j = cJSON_CreateObject();
    cJSON_AddNumberToObject(j, "wifi_applications", wifi_applications);
    cJSON_AddNumberToObject(j, "lte_applications", lte_applications);
    cJSON_AddNumberToObject(j, "ttl_applications", ttl_applications);
    return j;
  }
  return cJSON_Parse("{\"error\":\"unknown_method\"}");
}
static gboolean quit(gpointer data) {
  (void)data;
  g_main_loop_quit(loop);
  return G_SOURCE_REMOVE;
}
int main(int argc, char **argv) {
  if (argc > 1 && !strcmp(argv[1], "--check-ttl")) {
    puts(state_ttl_healthy() ? "healthy" : "missing_or_changed");
    return 0;
  }
  if (argc > 1 && !strcmp(argv[1], "--version")) {
    puts("microstick-networkd 1 event-driven");
    return 0;
  }
  umask(077);
  observe_only = argc > 1 && !strcmp(argv[1], "--observe");
  g_mkdir_with_parents(ev_run(), 0700);
  if (argc > 1 && !strcmp(argv[1], "--request"))
    return ev_client(argc, argv, "network.sock");
  char *p = ev_path(ev_run(), "network-daemon.lock");
  int lock = open(p, O_WRONLY | O_CREAT | O_CLOEXEC, 0600);
  g_free(p);
  if (lock < 0 || flock(lock, LOCK_EX | LOCK_NB))
    return 1;
  loop = g_main_loop_new(NULL, FALSE);
  if (ev_server("network.sock", handle, NULL) < 0)
    return 1;
  wifi_guard_init(changed);
  discovery_init(changed);
  ev_watch_files(changed, NULL);
  ev_watch_routes(changed, NULL);
  g_unix_signal_add(SIGTERM, quit, NULL);
  g_unix_signal_add(SIGINT, quit, NULL);
  g_timeout_add_seconds(20, watchdog, NULL);
  changed(0, NULL);
  watchdog(NULL);
  g_main_loop_run(loop);
  GPid workers[] = {wifi_worker, lte_worker, ttl_worker};
  for (unsigned i = 0; i < G_N_ELEMENTS(workers); i++)
    if (workers[i]) {
      kill(workers[i], SIGTERM);
      waitpid(workers[i], NULL, 0);
    }
  ev_remove(ev_run(), "network.sock");
  close(lock);
  return 0;
}
