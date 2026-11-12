#include "discovery.h"
#include <signal.h>
#include <string.h>
#include <sys/stat.h>
#include <unistd.h>
static GDBusProxy *proxy;
static char *configured, *effective, *applied, *previous;
static double previous_until;
static gboolean setting;
static double retry_after;
static EvChanged changed;
static char *hostname(void) {
  char *name = ev_read(ev_etc(), "hostname");
  g_strstrip(name);
  size_t n = strlen(name);
  if (!n || n > 63 || name[0] == '-' || name[n - 1] == '-' ||
      strspn(name, "abcdefghijklmnopqrstuvwxyz0123456789-") != n) {
    g_free(name);
    name = g_strdup("microstick");
  }
  return name;
}
cJSON *discovery_status(void) {
  char *name = hostname();
  cJSON *j = cJSON_CreateObject();
  cJSON_AddStringToObject(j, "hostname", name);
  if (previous && ev_now() < previous_until) {
    cJSON_AddStringToObject(j, "previous_mdns_name", previous);
    cJSON_AddNumberToObject(j, "previous_valid_until", previous_until);
  }
  char *fqdn =
      effective ? g_strdup(effective) : g_strdup_printf("%s.local", name);
  cJSON_AddStringToObject(j, "mdns_name", fqdn);
  cJSON_AddBoolToObject(j, "mdns_ready", effective != NULL);
  g_free(fqdn);
  g_free(name);
  return j;
}
static void publish(void) {
  cJSON *j = discovery_status();
  ev_write_json(ev_run(), "device.json", j);
  cJSON_Delete(j);
  char *p = ev_path(ev_run(), "device.json");
  chown(p, 0, 405);
  chmod(p, 0640);
  g_free(p);
}
static void got_name(GObject *obj, GAsyncResult *res, gpointer data) {
  (void)data;
  GVariant *v = g_dbus_proxy_call_finish(G_DBUS_PROXY(obj), res, NULL);
  if (v) {
    const char *s;
    g_variant_get(v, "(&s)", &s);
    g_free(effective);
    effective = g_strdup(s);
    g_variant_unref(v);
    publish();
  }
}
static void refresh_name(void) {
  if (proxy)
    g_dbus_proxy_call(proxy, "GetHostNameFqdn", NULL, G_DBUS_CALL_FLAGS_NONE,
                      2000, NULL, got_name, NULL);
}
static void set_done(GObject *obj, GAsyncResult *res, gpointer data) {
  char *name = data;
  if (proxy != G_DBUS_PROXY(obj)) {
    g_free(name);
    return;
  }
  GVariant *v = g_dbus_proxy_call_finish(G_DBUS_PROXY(obj), res, NULL);
  setting = FALSE;
  if (v) {
    g_free(applied);
    applied = g_strdup(name);
    g_variant_unref(v);
    refresh_name();
  }
  if (!v)
    retry_after = ev_now() + 20;
  g_free(name);
  changed(1, NULL);
}
static void server_signal(GDBusProxy *p, char *sender, char *name,
                          GVariant *parameters, gpointer data) {
  (void)p;
  (void)sender;
  (void)parameters;
  (void)data;
  if (!strcmp(name, "StateChanged"))
    refresh_name();
}
static void got_proxy(GObject *obj, GAsyncResult *res, gpointer data) {
  (void)obj;
  (void)data;
  proxy = g_dbus_proxy_new_finish(res, NULL);
  if (proxy) {
    g_signal_connect(proxy, "g-signal", G_CALLBACK(server_signal), NULL);
    changed(1, NULL);
    refresh_name();
  }
}
static void appeared(GDBusConnection *bus, const char *name, const char *owner,
                     gpointer data) {
  (void)owner;
  (void)data;
  g_dbus_proxy_new(bus, G_DBUS_PROXY_FLAGS_DO_NOT_AUTO_START, NULL, name, "/",
                   "org.freedesktop.Avahi.Server", NULL, got_proxy, NULL);
}
static void vanished(GDBusConnection *bus, const char *name, gpointer data) {
  (void)bus;
  (void)name;
  (void)data;
  setting = FALSE;
  g_clear_object(&proxy);
  g_clear_pointer(&effective, g_free);
  g_clear_pointer(&applied, g_free);
  publish();
}
void discovery_init(EvChanged callback) {
  changed = callback;
  g_bus_watch_name(G_BUS_TYPE_SYSTEM, "org.freedesktop.Avahi",
                   G_BUS_NAME_WATCHER_FLAGS_NONE, appeared, vanished, NULL,
                   NULL);
}
void discovery_tick(void) {
  char *name = hostname();
  if (!configured || strcmp(configured, name)) {
    if (configured) {
      g_free(previous);
      previous = effective ? g_strdup(effective)
                           : g_strdup_printf("%s.local", configured);
      previous_until = ev_now() + 120;
    }
    g_free(configured);
    configured = g_strdup(name);
    g_clear_pointer(&effective, g_free);
    sethostname(name, strlen(name));
    publish();
  }
  if (proxy && !setting && ev_now() >= retry_after &&
      (!applied || strcmp(applied, name))) {
    setting = TRUE;
    g_dbus_proxy_call(proxy, "SetHostName", g_variant_new("(s)", name),
                      G_DBUS_CALL_FLAGS_NONE, 2000, NULL, set_done,
                      g_strdup(name));
  }
  char *usb = ev_read(ev_run(), "usb-iface"),
       *wifi = ev_read(ev_run(), "wifi-interface");
  g_strstrip(usb);
  g_strstrip(wifi);
  if (!ev_valid_iface(usb)) {
    g_free(usb);
    usb = g_strdup("usb0");
  }
  if (!ev_valid_iface(wifi) || !strcmp(wifi, "unavailable")) {
    g_free(wifi);
    wifi = g_strdup("wlan0");
  }
  char *config = g_strdup_printf(
      "[server]\nhost-name=%s\nuse-ipv4=yes\nuse-ipv6=no\nallow-interfaces=%s,%"
      "s\nenable-dbus=yes\n[wide-area]\nenable-wide-area=no\n[publish]"
      "\npublish-hinfo=no\npublish-workstation=no\npublish-aaaa-on-ipv4=no\n["
      "reflector]\nenable-"
      "reflector=no\n",
      name, usb, wifi);
  char *old = ev_read(ev_run(), "mdns.conf");
  if (strcmp(old, config) && ev_write(ev_run(), "mdns.conf", config)) {
    /* Avahi only reloads service files on HUP; restart it for interface
     * changes. */
    char *pid = ev_read("/run/avahi-daemon", "pid");
    long n = strtol(pid, NULL, 10);
    if (n > 1)
      kill(n, SIGTERM);
    g_free(pid);
  }
  g_free(old);
  g_free(config);
  g_free(usb);
  g_free(wifi);
  g_free(name);
}
