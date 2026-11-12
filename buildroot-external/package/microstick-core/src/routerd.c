#include "event.h"
#include <errno.h>
#include <fcntl.h>
#include <libiptc/libiptc.h>
#include <net/if.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/stat.h>
#include <sys/wait.h>
#include <unistd.h>

static GMainLoop *loop;
static guint scheduled, deadline_timer;
static GPid worker;
static GQueue jobs = G_QUEUE_INIT;
static unsigned applications, firewall_applications;
static double retry_after;
static int failure_delay = 5;
static char health_problem[128];
struct job {
  char *method, *value;
  cJSON *input;
  int fd;
};
static const char *helper(void) {
  const char *s = getenv("MICROSTICK_ROUTER");
  return s ? s : "/usr/libexec/microstick-router";
}
static void request_check(unsigned type, gpointer data);
static void item_file(cJSON *j, const char *key, const char *base,
                      const char *name) {
  char *s = ev_read(base, name);
  g_strstrip(s);
  cJSON_AddStringToObject(j, key, s);
  g_free(s);
}
static cJSON *inputs(gboolean firewall) {
  cJSON *j = cJSON_CreateObject();
  cJSON_AddItemToObject(j, "lan", ev_lan());
  const char *names[] = {"usb-iface", "wifi-interface", "lan-mode", NULL};
  for (int i = 0; names[i]; i++)
    item_file(j, names[i], ev_run(), names[i]);
  if (firewall) {
    cJSON *f = ev_json(ev_etc(), "firewall.json");
    if (!f)
      f = cJSON_Parse("{\"rules\":[]}");
    cJSON_AddItemToObject(j, "firewall", f);
    item_file(j, "uplink", ev_run(), "uplink");
    item_file(j, "lte-iface", ev_run(), "lte-iface");
    cJSON *addresses=ev_addresses(),*sta=cJSON_CreateArray(),*address;
    char *wifi=ev_read(ev_run(),"wifi-interface");g_strstrip(wifi);
    cJSON_ArrayForEach(address,addresses)if(!strcmp(ev_str(address,"ifname"),wifi))cJSON_AddItemToArray(sta,cJSON_Duplicate(address,1));
    cJSON_AddItemToObject(j,"sta_addresses",sta);cJSON_Delete(addresses);g_free(wifi);
    cJSON *b = ev_json(ev_run(), "client-bindings.json");
    cJSON *relevant = cJSON_CreateArray(), *client, *rule;
    cJSON_ArrayForEach(client, b) {
      cJSON_ArrayForEach(rule, cJSON_GetObjectItemCaseSensitive(f, "rules")) {
        if (cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(rule, "enabled")) &&
            *ev_str(rule, "mac") &&
            !strcmp(ev_str(rule, "mac"), ev_str(client, "mac")) &&
            !strcmp(ev_str(rule, "network"), ev_str(client, "network"))) {
          cJSON_AddItemToArray(relevant, cJSON_Duplicate(client, 1));
          break;
        }
      }
    }
    cJSON_Delete(b);
    cJSON_AddItemToObject(j, "bindings", relevant);
  }
  return j;
}
static char *firewall_health(void) {
  health_problem[0] = 0;
  GChecksum *sum = g_checksum_new(G_CHECKSUM_SHA256);
  const char *tables[] = {"filter", "nat", "mangle"};
  gboolean ok = TRUE;
  for (int i = 0; i < 3; i++) {
    struct xtc_handle *h = iptc_init(tables[i]);
    if (!h) {
      snprintf(health_problem, sizeof(health_problem), "table %s errno %d",
               tables[i], errno);
      ok = FALSE;
      break;
    }
    const char *chains = i == 0   ? "MICROSTICK_INPUT,MICROSTICK_FORWARD"
                         : i == 1 ? "MICROSTICK_NAT"
                                  : "";
    gchar **list = g_strsplit(chains, ",", -1);
    for (int k = 0; list[k] && *list[k]; k++) {
      if (!iptc_is_chain(list[k], h)) {
        snprintf(health_problem, sizeof(health_problem), "chain %s", list[k]);
        ok = FALSE;
        break;
      }
      for (const struct ipt_entry *e = iptc_first_rule(list[k], h); e;
           e = iptc_next_rule(e, h)) {
        /* Kernel/userspace extension headers contain padding: hashing their raw
         * union makes identical rules differ between the daemon and a helper.
         * Hash semantic fields and extension data, never counters or headers.
         */
        const struct ipt_ip *ip = &e->ip;
        g_checksum_update(sum, (void *)&ip->src, sizeof(ip->src));
        g_checksum_update(sum, (void *)&ip->dst, sizeof(ip->dst));
        g_checksum_update(sum, (void *)&ip->smsk, sizeof(ip->smsk));
        g_checksum_update(sum, (void *)&ip->dmsk, sizeof(ip->dmsk));
        g_checksum_update(sum, (void *)ip->iniface,
                          strnlen(ip->iniface, IFNAMSIZ) + 1);
        g_checksum_update(sum, (void *)ip->outiface,
                          strnlen(ip->outiface, IFNAMSIZ) + 1);
        g_checksum_update(sum, ip->iniface_mask, IFNAMSIZ);
        g_checksum_update(sum, ip->outiface_mask, IFNAMSIZ);
        g_checksum_update(sum, (void *)&ip->proto, sizeof(ip->proto));
        g_checksum_update(sum, &ip->flags, sizeof(ip->flags));
        g_checksum_update(sum, &ip->invflags, sizeof(ip->invflags));
        for (unsigned off = sizeof(*e); off < e->target_offset;) {
          const struct xt_entry_match *m = (void *)((const char *)e + off);
          if (m->u.match_size < sizeof(*m) ||
              off + m->u.match_size > e->target_offset) {
            ok = FALSE;
            break;
          }
          g_checksum_update(sum, (void *)m->u.user.name,
                            strnlen(m->u.user.name, sizeof(m->u.user.name)));
          g_checksum_update(sum, &m->u.user.revision, 1);
          g_checksum_update(sum, m->data, m->u.match_size - sizeof(*m));
          off += m->u.match_size;
        }
        const struct xt_entry_target *t =
            (void *)((const char *)e + e->target_offset);
        const char *name = iptc_get_target(e, h);
        if (name)
          g_checksum_update(sum, (void *)name, strlen(name) + 1);
        g_checksum_update(sum, &t->u.user.revision, 1);
        if (*t->u.user.name)
          g_checksum_update(sum, t->data, t->u.target_size - sizeof(*t));
      }
    }
    g_strfreev(list);
    const char *hooks = i == 0   ? "INPUT,FORWARD"
                        : i == 1 ? "POSTROUTING"
                                 : "FORWARD";
    list = g_strsplit(hooks, ",", -1);
    for (int k = 0; list[k]; k++) {
      gboolean found = FALSE;
      for (const struct ipt_entry *e = iptc_first_rule(list[k], h); e;
           e = iptc_next_rule(e, h)) {
        const char *target = iptc_get_target(e, h);
        if (target && ((i == 0 && g_str_has_prefix(target, "MICROSTICK_")) ||
                       (i == 1 && !strcmp(target, "MICROSTICK_NAT")) ||
                       (i == 2 && !strcmp(target, "TCPMSS"))))
          found = TRUE;
      }
      if (!found) {
        snprintf(health_problem, sizeof(health_problem), "hook %s in %s",
                 list[k], tables[i]);
        ok = FALSE;
      }
    }
    g_strfreev(list);
    iptc_free(h);
    if (!ok)
      break;
  }
  char *s = g_strdup(ok ? g_checksum_get_string(sum) : "");
  g_checksum_free(sum);
  return s;
}
static void stamp(gboolean lan) {
  cJSON *j = inputs(TRUE);
  ev_write_json(ev_run(), "router-fw-inputs.json", j);
  cJSON_Delete(j);
  char *s = firewall_health();
  ev_write(ev_run(), "router-fw-health", s);
  g_free(s);
  ev_remove(ev_run(), "router-fw-repair");
  if (lan) {
    j = inputs(FALSE);
    ev_write_json(ev_run(), "router-lan-inputs.json", j);
    cJSON_Delete(j);
  }
}
static gboolean matches(const char *name, gboolean firewall) {
  cJSON *a = ev_json(ev_run(), name), *b = inputs(firewall);
  gboolean ok = a && cJSON_Compare(a, b, TRUE);
  cJSON_Delete(a);
  cJSON_Delete(b);
  return ok;
}
static gboolean lan_health(void) {
  if (!ev_alive("dnsmasq.pid"))
    return FALSE;
  cJSON *lan = ev_lan();
  gboolean ok = TRUE;
  char *mode = ev_read(ev_run(), "lan-mode");
  g_strstrip(mode);
  const char *keys[] = {"usb", "ap"};
  for (int i = 0; i < 2; i++) {
    char *iface = ev_read(ev_run(), i ? "wifi-interface" : "usb-iface");
    g_strstrip(iface);
    if ((!i || !strcmp(mode, "ap")) && if_nametoindex(iface) &&
        !ev_address(
            iface,
            ev_str(cJSON_GetObjectItemCaseSensitive(lan, keys[i]), "address")))
      ok = FALSE;
    g_free(iface);
  }
  g_free(mode);
  cJSON_Delete(lan);
  return ok;
}
static void bindings(void) {
  cJSON *c = ev_clients(), *b = ev_bindings(c);
  ev_write_json(ev_run(), "client-bindings.json", b);
  cJSON_Delete(b);
  cJSON_Delete(c);
}
static void start_job(void);
static void finished(GPid pid, gint status, gpointer data) {
  struct job *j = data;
  g_spawn_close_pid(pid);
  worker = 0;
  if (status == 0) {
    failure_delay = 5;
    retry_after = 0;
  } else {
    retry_after = ev_now() + failure_delay;
    failure_delay = MIN(60, failure_delay * 2);
  }
  if (j->fd >= 0) {
    cJSON *r = ev_json(ev_run(), "router-native-result.json");
    if (!r)
      r = cJSON_Parse(status ? "{\"error\":\"router_apply_failed\"}"
                             : "{\"ok\":true}");
    ev_reply(j->fd, r);
  }
  g_free(j->method);
  g_free(j->value);
  cJSON_Delete(j->input);
  g_free(j);
  request_check(1, NULL);
  start_job();
}
static void start_job(void) {
  if (worker || g_queue_is_empty(&jobs))
    return;
  struct job *j = g_queue_pop_head(&jobs);
  bindings();
  if (j->input)
    ev_write_json(ev_run(), "router-native-request.json", j->input);
  char *request = ev_path(ev_run(), "router-native-request.json"),
       *result = ev_path(ev_run(), "router-native-result.json");
  int out = open(result, O_WRONLY | O_CREAT | O_TRUNC | O_CLOEXEC, 0600);
  char **env = g_get_environ();
  env = g_environ_setenv(env, "MICROSTICK_ROUTER_WORKER", "yes", TRUE);
  char *repair = ev_read(ev_run(), "router-fw-repair");
  if (*repair)
    env = g_environ_setenv(env, "MICROSTICK_FIREWALL_FORCE", "yes", TRUE);
  g_free(repair);
  char *argv[] = {(char *)helper(), j->method,
                  j->input   ? request
                  : j->value ? j->value
                             : "",
                  NULL};
  gboolean ok = out >= 0 && g_spawn_async_with_fds(
                                NULL, argv, env, G_SPAWN_DO_NOT_REAP_CHILD,
                                NULL, NULL, &worker, -1, out, -1, NULL);
  if (out >= 0)
    close(out);
  g_strfreev(env);
  g_free(request);
  g_free(result);
  if (ok) {
    if (!strcmp(j->method, "apply-firewall"))
      firewall_applications++;
    else
      applications++;
    g_child_watch_add(worker, finished, j);
  } else {
    worker = 0;
    finished(0, 1, j);
  }
}
static void enqueue(const char *method, const char *value, cJSON *input,
                    int fd) {
  struct job *j = g_new0(struct job, 1);
  j->method = g_strdup(method);
  j->value = g_strdup(value);
  j->input = input ? cJSON_Duplicate(input, 1) : NULL;
  j->fd = fd;
  g_queue_push_tail(&jobs, j);
  start_job();
}
static gboolean expired(gpointer data) {
  (void)data;
  deadline_timer = 0;
  enqueue("tick", NULL, NULL, -1);
  return G_SOURCE_REMOVE;
}
static void deadline(void) {
  if (deadline_timer) {
    g_source_remove(deadline_timer);
    deadline_timer = 0;
  }
  cJSON *p = ev_json(ev_run(), "lan-pending.json");
  double end = ev_num(p, "deadline");
  cJSON_Delete(p);
  if (end > 0)
    deadline_timer =
        g_timeout_add((guint)MAX(1000, (end - ev_now()) * 1000), expired, NULL);
}
static gboolean check(gpointer data) {
  (void)data;
  scheduled = 0;
  deadline();
  char *wifi = ev_read(ev_run(), "wifi-interface");
  g_strstrip(wifi);
  ev_watch_wifi("/run/hostapd", wifi, request_check, NULL);
  g_free(wifi);
  if (worker || !g_queue_is_empty(&jobs) || ev_now() < retry_after)
    return G_SOURCE_REMOVE;
  bindings();
  if (!matches("router-lan-inputs.json", FALSE) || !lan_health()) {
    ev_write(ev_run(), "router-last-reason", "lan inputs or health");
    enqueue("tick", NULL, NULL, -1);
  } else if (!matches("router-fw-inputs.json", TRUE)) {
    ev_write(ev_run(), "router-last-reason", "firewall inputs");
    enqueue("apply-firewall", NULL, NULL, -1);
  }
  return G_SOURCE_REMOVE;
}
static void request_check(unsigned type, gpointer data) {
  (void)type;
  (void)data;
  if (!scheduled)
    scheduled = g_timeout_add(100, check, NULL);
}
static gboolean watchdog(gpointer data) {
  (void)data;
  if (!worker) {
    char *a = ev_read(ev_run(), "router-fw-health"), *b = firewall_health();
    if (!*b || strcmp(a, b)) {
      char *reason = g_strdup_printf("saved=%s current=%s problem=%s", a, b,
                                     health_problem);
      ev_write(ev_run(), "router-last-health", reason);
      g_free(reason);
      ev_remove(ev_run(), "router-fw-inputs.json");
      ev_write(ev_run(), "router-fw-repair", "yes");
    }
    g_free(a);
    g_free(b);
  }
  request_check(0, NULL);
  return G_SOURCE_CONTINUE;
}
static cJSON *handle(cJSON *q, int fd, gpointer data) {
  (void)data;
  const char *m = ev_str(q, "method");
  if (!strcmp(m, "lan_config") || !strcmp(m, "public")) {
    cJSON *r = cJSON_CreateObject();
    cJSON_AddItemToObject(r, "config", ev_lan());
    cJSON *p = ev_json(ev_run(), "lan-pending.json"),
          *pending = cJSON_CreateNull();
    if (p) {
      cJSON_Delete(pending);
      pending = cJSON_CreateObject();
      cJSON_AddStringToObject(pending, "token", ev_str(p, "token"));
      cJSON_AddNumberToObject(pending, "remaining",
                              MAX(0, ev_num(p, "deadline") - ev_now()));
    }
    cJSON_Delete(p);
    cJSON_AddItemToObject(r, "pending", pending);
    item_file(r, "wifi_mode", ev_run(), "lan-mode");
    item_file(r, "error", ev_run(), "router-error");
    item_file(r, "notice", ev_run(), "router-notice");
    return r;
  }
  if (!strcmp(m, "firewall_config")) {
    cJSON *r = ev_json(ev_etc(), "firewall.json");
    return r ? r : cJSON_Parse("{\"rules\":[]}");
  }
  if (!strcmp(m, "clients")) {
    cJSON *page = cJSON_GetObjectItemCaseSensitive(q, "page");
    if (page &&
        (!cJSON_IsNumber(page) || page->valuedouble < 0 ||
         page->valuedouble > 4096 || page->valuedouble != page->valueint))
      return cJSON_Parse("{\"error\":\"invalid_page\"}");
    int n = page ? page->valueint : 0;
    cJSON *all = ev_clients(), *r = cJSON_CreateObject(),
          *list = cJSON_AddArrayToObject(r, "clients");
    int total = cJSON_GetArraySize(all);
    for (int i = n * 32; i < MIN(total, (n + 1) * 32); i++)
      cJSON_AddItemToArray(list,
                           cJSON_Duplicate(cJSON_GetArrayItem(all, i), 1));
    cJSON_AddNumberToObject(r, "page", n);
    cJSON_AddNumberToObject(r, "total", total);
    cJSON_Delete(all);
    return r;
  }
  if (!strcmp(m, "diagnostics")) {
    cJSON *r = cJSON_CreateObject();
    cJSON_AddNumberToObject(r, "applications", applications);
    cJSON_AddNumberToObject(r, "firewall_applications", firewall_applications);
    return r;
  }
  const char *allowed[] = {"stage", "confirm", "cancel",         "firewall",
                           "mode",  "tick",    "apply-firewall", NULL};
  for (int i = 0; allowed[i]; i++)
    if (!strcmp(m, allowed[i])) {
      enqueue(m, ev_str(q, "value"),
              cJSON_GetObjectItemCaseSensitive(q, "input"), fd);
      return NULL;
    }
  return cJSON_Parse("{\"error\":\"unknown_method\"}");
}
static gboolean quit(gpointer data) {
  (void)data;
  g_main_loop_quit(loop);
  return G_SOURCE_REMOVE;
}
int main(int argc, char **argv) {
  if (argc > 1 && !strcmp(argv[1], "--inputs")) {
    cJSON *j = inputs(TRUE);
    char *s = cJSON_PrintUnformatted(j);
    puts(s);
    free(s);
    cJSON_Delete(j);
    return 0;
  }
  if (argc > 1 && !strcmp(argv[1], "--health")) {
    char *s = firewall_health();
    printf("%s %s\n", s, health_problem);
    g_free(s);
    return 0;
  }
  if (argc > 1 && !strcmp(argv[1], "--version")) {
    puts("microstick-routerd 1 event-driven");
    return 0;
  }
  umask(077);
  g_mkdir_with_parents(ev_run(), 0700);
  if (argc > 1 && !strcmp(argv[1], "--request"))
    return ev_client(argc, argv, "router.sock");
  if (argc > 1 && !strcmp(argv[1], "--stamp")) {
    stamp(argc > 2 && !strcmp(argv[2], "lan"));
    return 0;
  }
  char *p = ev_path(ev_run(), "router-daemon.lock");
  int lock = open(p, O_WRONLY | O_CREAT | O_CLOEXEC, 0600);
  g_free(p);
  if (lock < 0 || flock(lock, LOCK_EX | LOCK_NB))
    return 1;
  loop = g_main_loop_new(NULL, FALSE);
  if (ev_server("router.sock", handle, NULL) < 0)
    return 1;
  ev_watch_files(request_check, NULL);
  ev_watch_routes(request_check, NULL);
  g_unix_signal_add(SIGTERM, quit, NULL);
  g_unix_signal_add(SIGINT, quit, NULL);
  g_timeout_add_seconds(20, watchdog, NULL);
  request_check(0, NULL);
  g_main_loop_run(loop);
  if (worker) {
    kill(worker, SIGTERM);
    waitpid(worker, NULL, 0);
  }
  ev_remove(ev_run(), "router.sock");
  close(lock);
  return 0;
}
