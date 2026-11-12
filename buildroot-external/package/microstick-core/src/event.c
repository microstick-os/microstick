#include "event.h"
#include <arpa/inet.h>
#include <errno.h>
#include <fcntl.h>
#include <ifaddrs.h>
#include <linux/netlink.h>
#include <linux/rtnetlink.h>
#include <net/if.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/inotify.h>
#include <sys/file.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/un.h>
#include <unistd.h>

const char *ev_run(void) {
  const char *s = getenv("MICROSTICK_RUN");
  return s ? s : "/run/microstick";
}
const char *ev_etc(void) {
  const char *s = getenv("MICROSTICK_ETC");
  return s ? s : "/etc/microstick";
}
char *ev_path(const char *base, const char *name) {
  return g_build_filename(base, name, NULL);
}
char *ev_read(const char *base, const char *name) {
  char *p = ev_path(base, name), *s = NULL;
  gsize n = 0;
  if (!g_file_get_contents(p, &s, &n, NULL) || n > 262144) {
    g_free(s);
    s = g_strdup("");
  }
  g_free(p);
  return s;
}
cJSON *ev_json(const char *base, const char *name) {
  char *s = ev_read(base, name);
  cJSON *j = cJSON_Parse(s);
  g_free(s);
  return j;
}
const char *ev_str(const cJSON *j, const char *key) {
  cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsString(v) ? v->valuestring : "";
}
double ev_num(const cJSON *j, const char *key) {
  cJSON *v = cJSON_GetObjectItemCaseSensitive(j, key);
  return cJSON_IsNumber(v) ? v->valuedouble : 0;
}
void ev_set(cJSON *j, const char *key, const char *value) {
  cJSON_DeleteItemFromObjectCaseSensitive(j, key);
  cJSON_AddStringToObject(j, key, value ? value : "");
}
gboolean ev_write(const char *base, const char *name, const char *value) {
  char *old = ev_read(base, name);
  gboolean same = !strcmp(old, value);
  g_free(old);
  char *p = ev_path(base, name);
  if (same && g_file_test(p, G_FILE_TEST_EXISTS)) {
    g_free(p);
    return TRUE;
  }
  char *tmp = g_strdup_printf("%s.%ld.tmp", p, (long)getpid());
  int fd =
      open(tmp, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC | O_NOFOLLOW, 0600);
  size_t n = strlen(value);
  gboolean ok = fd >= 0 && write(fd, value, n) == (ssize_t)n;
  if (ok && !strcmp(base,ev_etc())) ok=fsync(fd)==0;
  if (fd >= 0)
    close(fd);
  if (ok)
    ok = rename(tmp, p) == 0;
  if (ok && !strcmp(base,ev_etc())) {
    int dir=open(base,O_RDONLY|O_DIRECTORY|O_CLOEXEC);
    ok=dir>=0 && fsync(dir)==0; if(dir>=0)close(dir);
  }
  if (!ok)
    unlink(tmp);
  g_free(tmp);
  g_free(p);
  return ok;
}
gboolean ev_write_json(const char *base, const char *name, cJSON *j) {
  char *s = cJSON_PrintUnformatted(j);
  gboolean ok = s && ev_write(base, name, s);
  free(s);
  return ok;
}
void ev_remove(const char *base, const char *name) {
  char *p = ev_path(base, name);
  unlink(p);
  g_free(p);
}
char *ev_conf(const char *name, const char *key, const char *fallback) {
  char *s = ev_read(ev_etc(), name), **lines = g_strsplit(s, "\n", -1),
       *out = g_strdup(fallback);
  size_t n = strlen(key);
  for (int i = 0; lines[i]; i++)
    if (!strncmp(lines[i], key, n) && lines[i][n] == '=') {
      g_free(out);
      out = g_strdup(lines[i] + n + 1);
      g_strstrip(out);
      size_t z = strlen(out);
      if (z >= 2 && ((out[0] == '\'' && out[z - 1] == '\'') ||
                     (out[0] == '"' && out[z - 1] == '"'))) {
        memmove(out, out + 1, z - 2);
        out[z - 2] = 0;
      }
    }
  g_strfreev(lines);
  g_free(s);
  return out;
}
int ev_config_lock(void) {
  char *path=ev_path(ev_run(),"network-config.lock");
  int fd=open(path,O_WRONLY|O_CREAT|O_CLOEXEC|O_NOFOLLOW,0600);g_free(path);
  if(fd>=0&&flock(fd,LOCK_EX)){close(fd);fd=-1;}
  return fd;
}
void ev_config_unlock(int fd){if(fd>=0)close(fd);}
double ev_now(void) { return (double)g_get_monotonic_time() / 1000000; }
gboolean ev_alive(const char *name) {
  char *s = ev_read(ev_run(), name), *end;
  long p = strtol(s, &end, 10);
  gboolean ok = p > 1 && (*end == 0 || *end == '\n') && kill((pid_t)p, 0) == 0;
  g_free(s);
  return ok;
}
cJSON *ev_lan(void) {
  cJSON *pending = ev_json(ev_run(), "lan-pending.json"),
        *cfg = cJSON_GetObjectItemCaseSensitive(pending, "config");
  if (cfg) {
    cJSON *j = cJSON_Duplicate(cfg, 1);
    cJSON_Delete(pending);
    return j;
  }
  cJSON_Delete(pending);
  cfg = ev_json(ev_etc(), "lan.json");
  if (cJSON_IsObject(cfg))
    return cfg;
  cJSON_Delete(cfg);
  cfg = cJSON_CreateObject();
  const char *keys[] = {"usb", "ap"};
  for (int i = 0; i < 2; i++) {
    char *addr = ev_conf("network.conf", i ? "AP_ADDRESS" : "USB_ADDRESS",
                         i ? "192.168.100.1/24" : "192.168.68.1/24");
    char *prefix = g_strdup(addr), *p = strrchr(prefix, '.');
    if (p)
      *p = 0;
    cJSON *n = cJSON_AddObjectToObject(cfg, keys[i]);
    cJSON_AddStringToObject(n, "address", addr);
    cJSON_AddBoolToObject(n, "enabled", 1);
    char *a = g_strdup_printf("%s.10", prefix),
         *b = g_strdup_printf("%s.200", prefix);
    cJSON_AddStringToObject(n, "start", a);
    cJSON_AddStringToObject(n, "end", b);
    cJSON_AddNumberToObject(n, "lease", 3600);
    cJSON_AddArrayToObject(n, "dns");
    g_free(a);
    g_free(b);
    g_free(addr);
    g_free(prefix);
  }
  cJSON_AddArrayToObject(cfg, "reservations");
  return cfg;
}
gboolean ev_valid_iface(const char *s) {
  return s && *s && strlen(s) < IFNAMSIZ &&
         strspn(s, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456"
                   "789_.-") == strlen(s);
}
cJSON *ev_addresses(void) {
  cJSON *out = cJSON_CreateArray();
  struct ifaddrs *all = NULL;
  if (getifaddrs(&all))
    return out;
  for (struct ifaddrs *a = all; a; a = a->ifa_next) {
    if (!a->ifa_addr || a->ifa_addr->sa_family != AF_INET)
      continue;
    cJSON *row = NULL, *q;
    cJSON_ArrayForEach(q, out) if (!strcmp(ev_str(q, "ifname"), a->ifa_name))
        row = q;
    if (!row) {
      row = cJSON_CreateObject();
      cJSON_AddItemToArray(out, row);
      cJSON_AddNumberToObject(row, "ifindex", if_nametoindex(a->ifa_name));
      cJSON_AddStringToObject(row, "ifname", a->ifa_name);
      cJSON_AddArrayToObject(row, "addr_info");
    }
    char ip[INET_ADDRSTRLEN];
    inet_ntop(AF_INET, &((struct sockaddr_in *)a->ifa_addr)->sin_addr, ip,
              sizeof(ip));
    uint32_t mask =
        a->ifa_netmask
            ? ntohl(((struct sockaddr_in *)a->ifa_netmask)->sin_addr.s_addr)
            : 0;
    unsigned bits = 0;
    while (mask & 0x80000000u) {
      bits++;
      mask <<= 1;
    }
    cJSON *v = cJSON_CreateObject();
    cJSON_AddStringToObject(v, "family", "inet");
    cJSON_AddStringToObject(v, "local", ip);
    cJSON_AddNumberToObject(v, "prefixlen", bits);
    cJSON_AddItemToArray(cJSON_GetObjectItemCaseSensitive(row, "addr_info"), v);
  }
  freeifaddrs(all);
  return out;
}
gboolean ev_address(const char *iface, const char *cidr) {
  struct ifaddrs *all = NULL;
  gboolean ok = FALSE;
  if (getifaddrs(&all))
    return FALSE;
  for (struct ifaddrs *a = all; a; a = a->ifa_next) {
    if (strcmp(a->ifa_name, iface) || !a->ifa_addr ||
        a->ifa_addr->sa_family != AF_INET || !(a->ifa_flags & IFF_UP))
      continue;
    char ip[64], s[80];
    inet_ntop(AF_INET, &((struct sockaddr_in *)a->ifa_addr)->sin_addr, ip,
              sizeof(ip));
    uint32_t mask =
        ntohl(((struct sockaddr_in *)a->ifa_netmask)->sin_addr.s_addr);
    unsigned bits = 0;
    while (mask & 0x80000000u) {
      bits++;
      mask <<= 1;
    }
    snprintf(s, sizeof(s), "%s/%u", ip, bits);
    if (!strcmp(s, cidr))
      ok = TRUE;
  }
  freeifaddrs(all);
  return ok;
}
char *ev_uplink(void) {
  FILE *f = fopen("/proc/net/route", "r");
  char line[512], best[IFNAMSIZ] = "";
  unsigned best_metric = UINT32_MAX;
  if (f) {
    while (fgets(line, sizeof(line), f)) {
      char iface[IFNAMSIZ];
      unsigned dst, gw, flags, refs, use, metric;
      if (sscanf(line, "%15s %x %x %x %u %u %u", iface, &dst, &gw, &flags,
                 &refs, &use, &metric) == 7 &&
          dst == 0 && (flags & 1) && metric < best_metric) {
        best_metric = metric;
        snprintf(best, sizeof(best), "%s", iface);
      }
    }
    fclose(f);
  }
  return g_strdup(best);
}
int ev_netlink(unsigned groups) {
  int fd = socket(AF_NETLINK, SOCK_RAW | SOCK_CLOEXEC | SOCK_NONBLOCK,
                  NETLINK_ROUTE);
  struct sockaddr_nl a = {.nl_family = AF_NETLINK, .nl_groups = groups};
  if (fd < 0)
    return -1;
  if (bind(fd, (void *)&a, sizeof(a))) {
    close(fd);
    return -1;
  }
  return fd;
}
struct watch {
  EvChanged changed;
  gpointer data;
  int fd;
};
static gboolean files_ready(gint fd, GIOCondition cond, gpointer data) {
  (void)cond;
  struct watch *w = data;
  char buf[16384] __attribute__((aligned(8)));
  ssize_t n;
  while ((n = read(fd, buf, sizeof(buf))) > 0)
    for (char *p = buf; p < buf + n;) {
      struct inotify_event *e = (void *)p;
      /* Whitelist final inputs, never our own publications, lock files or .new
       * writes. */
      const char *names[] = {"network.conf",
                             "lan.json",
                             "firewall.json",
                             "ttl.conf",
                             "hostapd.conf",
                             "wpa_supplicant.conf",
                             "lte.json",
                             "lte.properties",
                             "imei.json",
                             "lan-pending.json",
                             "lan-mode",
                             "usb-iface",
                             "wifi-interface",
                             "uplink",
                             "lte-iface",
                             "lte-request",
                             "lte-retry",
                             "scan-active",
                             "scan.json",
                             "wifi-stage",
                             "wifi-candidate.conf",
                             "wifi-recovery.json",
                             "hostname",
                             "dnsmasq.leases",
                             "wifi-dns",
                             "lte-dns",
                             NULL};
      if (e->mask & IN_Q_OVERFLOW)
        w->changed(0, w->data);
      else if (e->len)
        for (int i = 0; names[i]; i++)
          if (!strcmp(e->name, names[i])) {
            w->changed(1, w->data);
            break;
          }
      p += sizeof(*e) + e->len;
    }
  return G_SOURCE_CONTINUE;
}
void ev_watch_files(EvChanged changed, gpointer data) {
  int fd = inotify_init1(IN_NONBLOCK | IN_CLOEXEC);
  if (fd < 0)
    return;
  struct watch *w = g_new0(struct watch, 1);
  w->changed = changed;
  w->data = data;
  w->fd = fd;
  inotify_add_watch(fd, ev_etc(),
                    IN_CLOSE_WRITE | IN_MOVED_TO | IN_DELETE | IN_CREATE);
  inotify_add_watch(fd, ev_run(),
                    IN_CLOSE_WRITE | IN_MOVED_TO | IN_DELETE | IN_CREATE);
  g_unix_fd_add(fd, G_IO_IN, files_ready, w);
}
static gboolean routes_ready(gint fd, GIOCondition cond, gpointer data) {
  (void)cond;
  struct watch *w = data;
  char buf[32768];
  ssize_t n;
  while ((n = recv(fd, buf, sizeof(buf), 0)) > 0)
    for (struct nlmsghdr *h = (void *)buf; NLMSG_OK(h, n); h = NLMSG_NEXT(h, n))
      w->changed(h->nlmsg_type, w->data);
  if (n < 0 && errno == ENOBUFS)
    w->changed(0, w->data);
  return G_SOURCE_CONTINUE;
}
void ev_watch_routes(EvChanged changed, gpointer data) {
  int fd = ev_netlink(RTMGRP_LINK | RTMGRP_IPV4_IFADDR | RTMGRP_IPV4_ROUTE |
                      RTMGRP_NEIGH);
  if (fd < 0)
    return;
  struct watch *w = g_new0(struct watch, 1);
  w->changed = changed;
  w->data = data;
  w->fd = fd;
  g_unix_fd_add(fd, G_IO_IN, routes_ready, w);
}
struct server {
  EvRequest callback;
  gpointer data;
};
void ev_reply(int fd, cJSON *j) {
  char *s = j ? cJSON_PrintUnformatted(j) : NULL;
  if (!s || strlen(s) >= EV_LIMIT) {
    free(s);
    s = strdup("{\"error\":\"response_too_large\"}");
  }
  send(fd, s, strlen(s), MSG_NOSIGNAL);
  free(s);
  cJSON_Delete(j);
  close(fd);
}
static gboolean client_ready(gint fd, GIOCondition cond, gpointer data) {
  struct server *s = data;
  if (!(cond & G_IO_IN)) {
    close(fd);
    return G_SOURCE_REMOVE;
  }
  char b[EV_LIMIT + 1] = {0};
  ssize_t n = recv(fd, b, EV_LIMIT, MSG_TRUNC);
  cJSON *q = n > 0 && n < EV_LIMIT ? cJSON_Parse(b) : NULL;
  cJSON *out = cJSON_IsObject(q)
                   ? s->callback(q, fd, s->data)
                   : cJSON_Parse("{\"error\":\"invalid_request\"}");
  cJSON_Delete(q);
  if (out)
    ev_reply(fd, out);
  return G_SOURCE_REMOVE;
}
static gboolean accept_ready(gint fd, GIOCondition cond, gpointer data) {
  (void)cond;
  int c;
  while ((c = accept4(fd, NULL, NULL, SOCK_NONBLOCK | SOCK_CLOEXEC)) >= 0) {
    struct ucred p;
    socklen_t n = sizeof(p);
    if (getsockopt(c, SOL_SOCKET, SO_PEERCRED, &p, &n) || p.uid != geteuid()) {
      close(c);
      continue;
    }
    g_unix_fd_add(c, G_IO_IN | G_IO_HUP | G_IO_ERR, client_ready, data);
  }
  return G_SOURCE_CONTINUE;
}
int ev_server(const char *name, EvRequest callback, gpointer data) {
  int fd = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_NONBLOCK | SOCK_CLOEXEC, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  char *p = ev_path(ev_run(), name);
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", p);
  unlink(p);
  g_free(p);
  if (fd < 0 || bind(fd, (void *)&a, sizeof(a)) || listen(fd, 16)) {
    if (fd >= 0)
      close(fd);
    return -1;
  }
  chmod(a.sun_path, 0600);
  struct server *s = g_new0(struct server, 1);
  s->callback = callback;
  s->data = data;
  g_unix_fd_add(fd, G_IO_IN, accept_ready, s);
  return fd;
}
char *ev_rpc(const char *name, const char *request, int timeout_ms) {
  int fd = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  char *p = ev_path(ev_run(), name);
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", p);
  g_free(p);
  char *b = g_malloc0(EV_LIMIT + 1);
  ssize_t n = -1;
  if (fd >= 0 && !connect(fd, (void *)&a, sizeof(a)) &&
      send(fd, request, strlen(request), MSG_NOSIGNAL) >= 0) {
    struct pollfd pollfd = {fd, POLLIN, 0};
    if (poll(&pollfd, 1, timeout_ms) > 0)
      n = recv(fd, b, EV_LIMIT, MSG_TRUNC);
  }
  if (fd >= 0)
    close(fd);
  if (n <= 0 || n >= EV_LIMIT)
    snprintf(b, EV_LIMIT, "{\"error\":\"service_unavailable\"}");
  return b;
}
int ev_client(int argc, char **argv, const char *name) {
  cJSON *q = cJSON_CreateObject();
  cJSON_AddStringToObject(q, "method", argc > 2 ? argv[2] : "status");
  if (argc > 3) {
    char *s = NULL;
    if (g_file_get_contents(argv[3], &s, NULL, NULL)) {
      cJSON *v = cJSON_Parse(s);
      if (v)
        cJSON_AddItemToObject(q, "input", v);
      g_free(s);
    } else
      cJSON_AddStringToObject(q, "value", argv[3]);
  }
  char *request = cJSON_PrintUnformatted(q);
  cJSON_Delete(q);
  char *r = ev_rpc(name, request, 180000);
  free(request);
  puts(r);
  cJSON *v = cJSON_Parse(r);
  int rc = !v || *ev_str(v, "error");
  cJSON_Delete(v);
  g_free(r);
  return rc;
}
static int ctrl_open(const char *dir, const char *iface, char **local) {
  if (!ev_valid_iface(iface))
    return -1;
  int fd = socket(AF_UNIX, SOCK_DGRAM | SOCK_CLOEXEC | SOCK_NONBLOCK, 0);
  if (fd < 0)
    return -1;
  static unsigned sequence;
  *local =
      g_strdup_printf("%s/ctrl-%ld-%u", ev_run(), (long)getpid(), ++sequence);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", *local);
  if (bind(fd, (void *)&a, sizeof(a))) {
    close(fd);
    g_free(*local);
    *local = NULL;
    return -1;
  }
  snprintf(a.sun_path, sizeof(a.sun_path), "%s/%s", dir, iface);
  if (connect(fd, (void *)&a, sizeof(a))) {
    close(fd);
    unlink(*local);
    g_free(*local);
    *local = NULL;
    return -1;
  }
  return fd;
}
char *ev_ctrl(const char *dir, const char *iface, const char *command) {
  char *local = NULL;
  int fd = ctrl_open(dir, iface, &local);
  char *b = g_malloc0(16385);
  if (fd < 0)
    return b;
  if (send(fd, command, strlen(command), MSG_NOSIGNAL) >= 0) {
    struct pollfd p = {fd, POLLIN, 0};
    if (poll(&p, 1, 100) > 0) {
      ssize_t n = recv(fd, b, 16384, 0);
      if (n > 0)
        b[n] = 0;
    }
  }
  close(fd);
  unlink(local);
  g_free(local);
  return b;
}
struct wifi_watch {
  EvChanged changed;
  gpointer data;
  char *path;
  int fd;
};
struct control_identity {
  dev_t device;
  ino_t inode;
  guint source;
};
static void identity_free(gpointer data) {
  struct control_identity *v = data;
  g_source_remove(v->source);
  g_free(v);
}
static gboolean wifi_ready(gint fd, GIOCondition cond, gpointer data) {
  struct wifi_watch *w = data;
  char b[16384];
  (void)cond;
  while (recv(fd, b, sizeof(b), 0) > 0)
    w->changed(2, w->data);
  return G_SOURCE_CONTINUE;
}
static void wifi_free(gpointer data) {
  struct wifi_watch *w = data;
  close(w->fd);
  unlink(w->path);
  g_free(w->path);
  g_free(w);
}
void ev_watch_wifi(const char *dir, const char *iface, EvChanged changed,
                   gpointer data) {
  /* Retry on watchdog; a restarted control server gets a fresh socket identity.
   */
  static GHashTable *known;
  if (!known)
    known =
        g_hash_table_new_full(g_str_hash, g_str_equal, g_free, identity_free);
  char *path = g_strdup_printf("%s/%s", dir, iface);
  struct stat st;
  if (stat(path, &st)) {
    g_hash_table_remove(known, path);
    g_free(path);
    return;
  }
  struct control_identity *previous = g_hash_table_lookup(known, path);
  if (previous && previous->device == st.st_dev &&
      previous->inode == st.st_ino) {
    g_free(path);
    return;
  }
  char *local = NULL;
  int fd = ctrl_open(dir, iface, &local);
  if (fd < 0) {
    g_free(path);
    return;
  }
  if (send(fd, "ATTACH", 6, MSG_NOSIGNAL) < 0) {
    close(fd);
    unlink(local);
    g_free(local);
    g_free(path);
    return;
  }
  struct wifi_watch *w = g_new0(struct wifi_watch, 1);
  w->changed = changed;
  w->data = data;
  w->path = local;
  w->fd = fd;
  struct control_identity *identity = g_new0(struct control_identity, 1);
  identity->device = st.st_dev;
  identity->inode = st.st_ino;
  identity->source = g_unix_fd_add_full(G_PRIORITY_DEFAULT, fd, G_IO_IN,
                                        wifi_ready, w, wifi_free);
  g_hash_table_replace(known, path, identity);
}
