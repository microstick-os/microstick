#include "event.h"
#include <arpa/inet.h>
#include <linux/neighbour.h>
#include <linux/rtnetlink.h>
#include <net/if.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <time.h>
#include <unistd.h>

/* Bounded observations, no authentication or speculative MAC/IP ownership. */
#define ROWS 512
struct row {
  char network[4], mac[18], ip[16], hostname[65];
  double expires;
  int priority;
  gboolean active, station;
};
static struct row rows[ROWS];
static unsigned count;
static gboolean mac_valid(const char *s) {
  unsigned x[6];
  char end;
  return strlen(s) == 17 && sscanf(s, "%2x:%2x:%2x:%2x:%2x:%2x%c", x, x + 1,
                                   x + 2, x + 3, x + 4, x + 5, &end) == 6;
}
static void add(const char *network, const char *mac, const char *ip,
                const char *host, double expires, int priority, gboolean active,
                gboolean station) {
  struct in_addr a;
  if (count == ROWS || !mac_valid(mac) ||
      (*ip && inet_pton(AF_INET, ip, &a) != 1))
    return;
  struct row *r = rows + count++;
  snprintf(r->network, sizeof(r->network), "%s", network);
  snprintf(r->mac, sizeof(r->mac), "%s", mac);
  for (char *p = r->mac; *p; p++)
    *p = g_ascii_tolower(*p);
  snprintf(r->ip, sizeof(r->ip), "%s", ip);
  snprintf(r->hostname, sizeof(r->hostname), "%s",
           !strcmp(host, "*") ? "" : host);
  r->expires = expires;
  r->priority = priority;
  r->active = active;
  r->station = station;
}
static gboolean subnet(const char *ip, const char *cidr) {
  char s[64];
  snprintf(s, sizeof(s), "%s", cidr);
  char *p = strchr(s, '/');
  if (!p)
    return FALSE;
  *p++ = 0;
  int bits = atoi(p);
  struct in_addr a, b;
  if (bits < 1 || bits > 32 || inet_pton(AF_INET, ip, &a) != 1 ||
      inet_pton(AF_INET, s, &b) != 1)
    return FALSE;
  uint32_t mask = htonl(UINT32_MAX << (32 - bits));
  return (a.s_addr & mask) == (b.s_addr & mask);
}
static void neighbors(const char *usb, const char *wifi, gboolean ap) {
  int fd = ev_netlink(0);
  if (fd < 0)
    return;
  struct {
    struct nlmsghdr h;
    struct ndmsg n;
  } q = {.h = {.nlmsg_len = NLMSG_LENGTH(sizeof(struct ndmsg)),
               .nlmsg_type = RTM_GETNEIGH,
               .nlmsg_flags = NLM_F_REQUEST | NLM_F_DUMP,
               .nlmsg_seq = 1},
         .n = {.ndm_family = AF_INET}};
  struct sockaddr_nl kernel = {.nl_family = AF_NETLINK};
  if (sendto(fd, &q, sizeof(q), 0, (void *)&kernel, sizeof(kernel)) < 0) {
    close(fd);
    return;
  }
  double until = ev_now() + .1;
  gboolean done = FALSE;
  while (!done && ev_now() < until) {
    struct pollfd p = {fd, POLLIN, 0};
    if (poll(&p, 1, 20) <= 0)
      continue;
    char b[32768];
    ssize_t n = recv(fd, b, sizeof(b), 0);
    for (struct nlmsghdr *h = (void *)b; NLMSG_OK(h, n); h = NLMSG_NEXT(h, n)) {
      if (h->nlmsg_type == NLMSG_DONE || h->nlmsg_type == NLMSG_ERROR) {
        done = TRUE;
        break;
      }
      if (h->nlmsg_type != RTM_NEWNEIGH)
        continue;
      struct ndmsg *m = NLMSG_DATA(h);
      char iface[IFNAMSIZ];
      if (!if_indextoname(m->ndm_ifindex, iface))
        continue;
      const char *network = !strcmp(iface, usb)          ? "usb"
                            : ap && !strcmp(iface, wifi) ? "ap"
                                                         : NULL;
      if (!network)
        continue;
      unsigned char *mac = NULL;
      struct in_addr *ip = NULL;
      int len = NLMSG_PAYLOAD(h, sizeof(*m));
      for (struct rtattr *a = (void *)((char *)m + NLMSG_ALIGN(sizeof(*m)));
           RTA_OK(a, len); a = RTA_NEXT(a, len)) {
        if (a->rta_type == NDA_LLADDR && RTA_PAYLOAD(a) >= 6)
          mac = RTA_DATA(a);
        if (a->rta_type == NDA_DST && RTA_PAYLOAD(a) >= 4)
          ip = RTA_DATA(a);
      }
      if (mac && ip) {
        char ms[18], is[16];
        snprintf(ms, sizeof(ms), "%02x:%02x:%02x:%02x:%02x:%02x", mac[0],
                 mac[1], mac[2], mac[3], mac[4], mac[5]);
        inet_ntop(AF_INET, ip, is, sizeof(is));
        gboolean active =
            m->ndm_state & (NUD_REACHABLE | NUD_DELAY | NUD_PROBE);
        add(network, ms, is, "", 0, active ? 3 : 1, active, FALSE);
      }
    }
  }
  close(fd);
}
static int compare(const void *a, const void *b) {
  const struct row *x = a, *y = b;
  int n = strcmp(x->network, y->network);
  return n ? n : strcmp(x->mac, y->mac);
}
static int ip_compare(const void *a, const void *b) {
  return strcmp((*(cJSON *const *)a)->valuestring,
                (*(cJSON *const *)b)->valuestring);
}
static int client_compare(const void *a, const void *b) {
  const cJSON *x = *(cJSON *const *)a, *y = *(cJSON *const *)b;
  int n = strcmp(ev_str(x, "network"), ev_str(y, "network"));
  if (!n)
    n = strcmp(ev_str(x, "name"), ev_str(y, "name"));
  return n ? n : strcmp(ev_str(x, "mac"), ev_str(y, "mac"));
}
static void sort_array(cJSON *a, int (*cmp)(const void *, const void *)) {
  int n = cJSON_GetArraySize(a);
  if (n < 2)
    return;
  cJSON **items = g_new(cJSON *, n);
  for (int i = 0; i < n; i++)
    items[i] = cJSON_DetachItemFromArray(a, 0);
  qsort(items, n, sizeof(*items), cmp);
  for (int i = 0; i < n; i++)
    cJSON_AddItemToArray(a, items[i]);
  g_free(items);
}
cJSON *ev_clients(void) {
  count = 0;
  memset(rows, 0, sizeof(rows));
  char *usb = ev_read(ev_run(), "usb-iface"),
       *wifi = ev_read(ev_run(), "wifi-interface"),
       *mode = ev_read(ev_run(), "lan-mode");
  g_strstrip(usb);
  g_strstrip(wifi);
  g_strstrip(mode);
  neighbors(usb, wifi, !strcmp(mode, "ap"));
  cJSON *lan = ev_lan(),
        *reservations = cJSON_GetObjectItemCaseSensitive(lan, "reservations"),
        *r;
  char *leasepath = ev_path(ev_run(), "dnsmasq.leases");
  FILE *f = fopen(leasepath, "r");
  g_free(leasepath);
  double now = (double)time(NULL);
  if (f) {
    char line[512];
    while (fgets(line, sizeof(line), f)) {
      double expires;
      char mac[18], ip[16], host[256];
      if (sscanf(line, "%lf %17s %15s %255s", &expires, mac, ip, host) != 4 ||
          (expires != 0 && expires <= now))
        continue;
      const char *keys[] = {"usb", "ap"};
      for (int i = 0; i < 2; i++)
        if (subnet(ip, ev_str(cJSON_GetObjectItemCaseSensitive(lan, keys[i]),
                              "address")))
          add(keys[i], mac, ip, host, expires, 2, FALSE, FALSE);
    }
    fclose(f);
  }
  cJSON_ArrayForEach(r, reservations)
      add(ev_str(r, "network"), ev_str(r, "mac"), ev_str(r, "ip"), "", 0, 0,
          FALSE, FALSE);
  if (!strcmp(mode, "ap")) {
    char *s = ev_ctrl("/run/hostapd", wifi, "STA-FIRST");
    unsigned limit = 0;
    while (*s && strncmp(s, "FAIL", 4) && limit++ < 64) {
      char *end = strchr(s, '\n');
      if (end)
        *end = 0;
      if (!mac_valid(s))
        break;
      add("ap", s, "", "", 0, 0, TRUE, TRUE);
      char *cmd = g_strdup_printf("STA-NEXT %s", s);
      g_free(s);
      s = ev_ctrl("/run/hostapd", wifi, cmd);
      g_free(cmd);
    }
    g_free(s);
  }
  qsort(rows, count, sizeof(rows[0]), compare);
  cJSON *out = cJSON_CreateArray();
  for (unsigned i = 0; i < count;) {
    unsigned end = i + 1;
    while (end < count && !compare(rows + i, rows + end))
      end++;
    cJSON *o = cJSON_CreateObject(), *ips = cJSON_AddArrayToObject(o, "ips");
    char combined[1024] = "", hostname[65] = "";
    gboolean active = FALSE, station = FALSE;
    double expires = 0;
    for (unsigned k = i; k < end; k++) {
      struct row *x = rows + k;
      active |= x->active;
      station |= x->station;
      if (x->expires > expires)
        expires = x->expires;
      if (*x->hostname)
        snprintf(hostname, sizeof(hostname), "%s", x->hostname);
      if (!*x->ip)
        continue;
      gboolean owner = TRUE;
      for (unsigned n = 0; n < count; n++)
        if (n != k && !strcmp(rows[n].network, x->network) &&
            !strcmp(rows[n].ip, x->ip) &&
            (rows[n].priority > x->priority ||
             (rows[n].priority == x->priority &&
              strcmp(rows[n].mac, x->mac) > 0)))
          owner = FALSE;
      gboolean duplicate = FALSE;
      cJSON *v;
      cJSON_ArrayForEach(v, ips) if (!strcmp(v->valuestring, x->ip)) duplicate =
          TRUE;
      if (owner && !duplicate) {
        cJSON_AddItemToArray(ips, cJSON_CreateString(x->ip));
        if (*combined)
          g_strlcat(combined, ", ", sizeof(combined));
        g_strlcat(combined, x->ip, sizeof(combined));
      }
    }
    cJSON *saved = NULL;
    sort_array(ips, ip_compare);
    combined[0] = 0;
    cJSON *ip;
    cJSON_ArrayForEach(ip, ips) {
      if (*combined)
        g_strlcat(combined, ", ", sizeof(combined));
      g_strlcat(combined, ip->valuestring, sizeof(combined));
    }
    cJSON_ArrayForEach(
        r, reservations) if (!strcmp(ev_str(r, "network"), rows[i].network) &&
                             !g_ascii_strcasecmp(ev_str(r, "mac"), rows[i].mac))
        saved = r;
    cJSON_AddStringToObject(o, "network", rows[i].network);
    cJSON_AddStringToObject(o, "mac", rows[i].mac);
    cJSON_AddStringToObject(o, "ip", combined);
    cJSON_AddStringToObject(o, "name", ev_str(saved, "name"));
    cJSON_AddStringToObject(o, "hostname", hostname);
    cJSON_AddBoolToObject(o, "reserved", *ev_str(saved, "ip") != 0);
    cJSON_AddNumberToObject(o, "expires", expires);
    cJSON_AddStringToObject(o, "activity",
                            station  ? "connected"
                            : active ? "recent"
                                     : "lease_only");
    cJSON_AddItemToArray(out, o);
    i = end;
  }
  cJSON_Delete(lan);
  g_free(usb);
  g_free(wifi);
  g_free(mode);
  sort_array(out, client_compare);
  return out;
}
cJSON *ev_bindings(cJSON *clients) {
  cJSON *out = cJSON_CreateArray(), *c;
  cJSON_ArrayForEach(c, clients) {
    cJSON *r = cJSON_CreateObject();
    cJSON_AddStringToObject(r, "network", ev_str(c, "network"));
    cJSON_AddStringToObject(r, "mac", ev_str(c, "mac"));
    cJSON_AddItemToObject(
        r, "ips",
        cJSON_Duplicate(cJSON_GetObjectItemCaseSensitive(c, "ips"), 1));
    cJSON_AddItemToArray(out, r);
  }
  return out;
}
