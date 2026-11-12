/* SPDX-License-Identifier: GPL-2.0-or-later */
#define _GNU_SOURCE
#include "model.h"
#include "nft.h"
#include <cjson/cJSON.h>
#include <dirent.h>
#include <errno.h>
#include <fcntl.h>
#include <linux/magic.h>
#include <poll.h>
#include <signal.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/inotify.h>
#include <sys/resource.h>
#include <sys/socket.h>
#include <sys/stat.h>
#include <sys/timerfd.h>
#include <sys/timex.h>
#include <sys/un.h>
#include <sys/vfs.h>
#include <time.h>
#include <unistd.h>
#define LIMIT 32768
static volatile sig_atomic_t quitting;
static struct model model, wifi_history;
static int wifi_persistence, wifi_invalid;
static char boot[40], epoch[80], data_dir[512], runtime[512], socket_name[108],
    ram_path[600];
static char wifi_name[160], wifi_iface[32], error[64], thermal[256];
static double cpu, ram_used, ram_total, temp = -1, last_system;
static uint64_t old_cpu, old_idle;
static int trusted;
static void stop(int s) {
  (void)s;
  quitting = 1;
}
static double monotime(void) {
  struct timespec ts;
  clock_gettime(CLOCK_MONOTONIC, &ts);
  return ts.tv_sec + ts.tv_nsec / 1e9;
}
static int textfile(const char *p, char *b, size_t cap) {
  FILE *f = fopen(p, "r");
  if (!f) {
    *b = 0;
    return -1;
  }
  size_t n = fread(b, 1, cap - 1, f);
  b[n] = 0;
  fclose(f);
  b[strcspn(b, "\r\n")] = 0;
  return 0;
}
static const char *core_run(void) {const char *p=getenv("MICROSTICK_RUN");return p?p:"/run/microstick";}
static const char *core_path(const char *name) {static char path[600];snprintf(path,sizeof(path),"%s/%s",core_run(),name);return path;}
static const char *str(cJSON *j, const char *k) {
  const cJSON *v = cJSON_GetObjectItemCaseSensitive(j, k);
  return cJSON_IsString(v) ? v->valuestring : "";
}
static void bytes(cJSON *j, const char *k, uint64_t n) {
  char b[32];
  snprintf(b, sizeof(b), "%llu", (unsigned long long)n);
  cJSON_AddStringToObject(j, k, b);
}
struct ram_image {
  uint64_t magic, hash;
  char boot[40], epoch[80];
  struct model model, wifi_history;
};
static void ram_save(void) {
  struct ram_image r = {.magic = 2, .model = model, .wifi_history=wifi_history};
  snprintf(r.boot, sizeof(r.boot), "%s", boot);
  snprintf(r.epoch, sizeof(r.epoch), "%s", epoch);
  r.hash = checksum(&r, sizeof(r));
  char tmp[640];
  snprintf(tmp, sizeof(tmp), "%s.new", ram_path);
  int fd =
      open(tmp, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW | O_CLOEXEC, 0600);
  if (fd < 0)
    return;
  if (write(fd, &r, sizeof(r)) == (ssize_t)sizeof(r)) {
    close(fd);
    rename(tmp, ram_path);
  } else {
    close(fd);
    unlink(tmp);
  }
}
static void ram_load(void) {
  struct ram_image r;
  int fd = open(ram_path, O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
  if (fd < 0)
    return;
  ssize_t n = read(fd, &r, sizeof(r));
  close(fd);
  if (n != (ssize_t)sizeof(r) || r.magic != 2)
    return;
  uint64_t hash = r.hash;
  r.hash = 0;
  if (hash != checksum(&r, sizeof(r)) || strcmp(r.boot, boot) ||
      strcmp(r.epoch, epoch) || r.model.d.generation < model.d.generation ||
      r.model.count > POINTS || r.model.pos >= POINTS)
    return;
  model = r.model;
  if(r.wifi_history.d.generation>=wifi_history.d.generation)wifi_history=r.wifi_history;
}
static void source(void) {
  char mode[32], iface[32], status[8192];
  textfile(core_path("lan-mode"), mode, sizeof(mode));
  textfile(core_path("wifi-interface"), iface, sizeof(iface));
  textfile(core_path("wifi-status"), status, sizeof(status));
  /* wifi-status is multiline; read it without textfile's line trimming. */
  FILE *f = fopen(core_path("wifi-status"), "r");
  char state[32] = "", ssid[80] = "", bssid[32] = "";
  if (f) {
    char line[256];
    while (fgets(line, sizeof(line), f)) {
      line[strcspn(line, "\r\n")] = 0;
      if (!strncmp(line, "wpa_state=", 10))
        snprintf(state, sizeof(state), "%.31s", line + 10);
      else if (!strncmp(line, "ssid=", 5))
        snprintf(ssid, sizeof(ssid), "%.79s", line + 5);
      else if (!strncmp(line, "bssid=", 6))
        snprintf(bssid, sizeof(bssid), "%.31s", line + 6);
    }
    fclose(f);
  }
  int ok = !strcmp(mode, "sta") && !strcmp(state, "COMPLETED") && *iface &&
           strlen(iface) < sizeof(wifi_iface) &&
           strspn(iface, "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0"
                         "123456789_.-") == strlen(iface);
  char desired[32] = "";
  if (ok)
    snprintf(desired, sizeof(desired), "%s", iface);
  int change = counters_wifi(desired);
  if (change < 0) {
    snprintf(error, sizeof(error), "counter_unavailable");
    return;
  }

  snprintf(wifi_iface, sizeof(wifi_iface), "%s", desired);
  if (ok)
    snprintf(wifi_name, sizeof(wifi_name), "%s|%s", ssid, bssid);
  else
    wifi_name[0] = 0;
}
static void system_read(double mono) {
  if (mono - last_system < 10 && last_system)
    return;
  last_system = mono;
  FILE *f = fopen("/proc/stat", "r");
  uint64_t total = 0, idle = 0;
  if (f) {
    char line[512];
    if (fgets(line, sizeof(line), f)) {
      unsigned long long x[8] = {0};
      sscanf(line, "cpu %llu %llu %llu %llu %llu %llu %llu %llu", x, x + 1,
             x + 2, x + 3, x + 4, x + 5, x + 6, x + 7);
      for (int i = 0; i < 8; i++)
        total += x[i];
      idle = x[3] + x[4];
      if (old_cpu && total > old_cpu)
        cpu = 100.0 *
              (1.0 - (double)(idle - old_idle) / (double)(total - old_cpu));
      old_cpu = total;
      old_idle = idle;
    }
    fclose(f);
  }
  f = fopen("/proc/meminfo", "r");
  if (f) {
    char line[256];
    unsigned long long avail = 0;
    while (fgets(line, sizeof(line), f)) {
      unsigned long long n;
      if (sscanf(line, "MemTotal: %llu", &n) == 1)
        ram_total = (double)n * 1024;
      if (sscanf(line, "MemAvailable: %llu", &n) == 1)
        avail = n;
    }
    ram_used = ram_total - (double)avail * 1024;
    fclose(f);
  }
  if (!*thermal) {
    DIR *d = opendir("/sys/class/thermal");
    if (d) {
      struct dirent *e;
      while ((e = readdir(d))) {
        char p[512], type[80];
        if (strncmp(e->d_name, "thermal_zone", 12))
          continue;
        snprintf(p, sizeof(p), "/sys/class/thermal/%s/type", e->d_name);
        if (!textfile(p, type, sizeof(type)) &&
            !strcmp(type, "modem-thermal")) {
          snprintf(thermal, sizeof(thermal), "/sys/class/thermal/%.180s/temp",
                   e->d_name);
          break;
        }
      }
      closedir(d);
    }
  }
  if (*thermal) {
    char v[64];
    if (!textfile(thermal, v, sizeof(v)))
      temp = strtod(v, NULL) / 1000.0;
  }
}
static void sample(void) {
  source();
  uint64_t c[4];
  if (counters_read(c) < 0) {
    snprintf(error, sizeof(error), "counter_unavailable");
    return;
  }
  struct timex tx = {0};
  trusted = adjtimex(&tx) >= 0 && !(tx.status & STA_UNSYNC) &&
            time(NULL) > 1577836800;
  if (!trusted) {
    char synced_boot[40];
    if (!textfile(core_path("time-synced"), synced_boot,
                  sizeof(synced_boot)) &&
        !strcmp(synced_boot, boot) && time(NULL) > 1577836800)
      trusted = 1;
  }
  double mono = monotime();
  model_sample(&model, c, boot, epoch, wifi_name, mono, time(NULL), trusted);
  if(wifi_persistence&&!wifi_invalid){uint64_t wc[4]={c[2],c[3],0,0};model_sample(&wifi_history,wc,boot,epoch,"",mono,time(NULL),trusted);}
  system_read(mono);
  error[0] = 0;
  if(wifi_invalid)snprintf(error,sizeof(error),"wifi_statistics_invalid");
  if (model_due(&model, mono) && model_save(&model, data_dir))
    snprintf(error, sizeof(error), "statistics_save_failed");
  if(wifi_persistence&&!wifi_invalid&&model_due(&wifi_history,mono)&&model_save(&wifi_history,data_dir))snprintf(error,sizeof(error),"statistics_save_failed");
  ram_save();
}
static cJSON *status(void) {
  cJSON *j = cJSON_CreateObject();
  cJSON_AddNumberToObject(j, "api_version", 1);
  cJSON_AddStringToObject(j, "error", error);
  cJSON_AddBoolToObject(j, "clock_synced", trusted);
  time_t now = time(NULL);
  struct tm tm;
  localtime_r(&now, &tm);
  char tz[64];
  strftime(tz, sizeof(tz), "%Z %z", &tm);
  cJSON_AddStringToObject(j, "timezone", tz);
  cJSON *lte = cJSON_AddObjectToObject(j, "lte"),
        *wifi = cJSON_AddObjectToObject(j, "wifi"),
        *sys = cJSON_AddObjectToObject(j, "system");
  bytes(lte, "rx", model.d.rx - model.d.base_rx);
  bytes(lte, "tx", model.d.tx - model.d.base_tx);
  bytes(lte, "total_rx", model.d.rx);
  bytes(lte, "total_tx", model.d.tx);
  cJSON_AddNumberToObject(lte, "reset_at", (double)model.d.reset_at);
  char local_reset[48] = "";
  time_t reset_time = model.d.reset_at;
  struct tm reset_tm;
  if (reset_time && localtime_r(&reset_time, &reset_tm))
    strftime(local_reset, sizeof(local_reset), "%Y-%m-%d %H:%M", &reset_tm);
  cJSON_AddStringToObject(lte, "reset_local", local_reset);
  cJSON_AddNumberToObject(lte, "started_at", (double)model.d.started_at);
  bytes(lte, "undated_rx", model.d.undated_rx);
  bytes(lte, "undated_tx", model.d.undated_tx);
  cJSON_AddBoolToObject(wifi,"persistence",wifi_persistence);
  cJSON *saved=cJSON_AddObjectToObject(wifi,"saved");
  bytes(saved,"rx",wifi_history.d.rx-wifi_history.d.base_rx);bytes(saved,"tx",wifi_history.d.tx-wifi_history.d.base_tx);
  cJSON_AddNumberToObject(saved,"reset_at",wifi_history.d.reset_at);
  bytes(saved,"undated_rx",wifi_history.d.undated_rx);bytes(saved,"undated_tx",wifi_history.d.undated_tx);
  bytes(wifi, "rx", model.wifi_rx);
  bytes(wifi, "tx", model.wifi_tx);
  cJSON_AddBoolToObject(wifi, "connected", *wifi_name != 0);
  cJSON_AddNumberToObject(wifi, "session_seconds",
                          *wifi_name ? monotime() - model.wifi_started : 0);
  for (unsigned i = 0; i < 2; i++) {
    cJSON *s = i ? wifi : lte;
    double rx = 0, txv = 0;
    if (model.count) {
      struct point *p = &model.points[(model.pos + POINTS - 1) % POINTS];
      if (p->dt > 0) {
        rx = (double)p->rx[i] / p->dt;
        txv = (double)p->tx[i] / p->dt;
      }
    }
    cJSON_AddNumberToObject(s, "rx_bps", rx);
    cJSON_AddNumberToObject(s, "tx_bps", txv);
  }
  cJSON_AddNumberToObject(sys, "uptime", monotime());
  cJSON_AddNumberToObject(sys, "cpu_percent", cpu);
  cJSON_AddNumberToObject(sys, "memory_used", ram_used);
  cJSON_AddNumberToObject(sys, "memory_total", ram_total);
  if (temp >= 0)
    cJSON_AddNumberToObject(sys, "modem_temperature", temp);
  return j;
}
static int settings_save(int enabled) {
  char tmp[600],path[600];snprintf(path,sizeof(path),"%s/settings.json",data_dir);snprintf(tmp,sizeof(tmp),"%s/settings.json.new",data_dir);
  const char *body=enabled?"{\"wifi_persistence\":true}\n":"{\"wifi_persistence\":false}\n";
  int fd=open(tmp,O_WRONLY|O_CREAT|O_TRUNC|O_CLOEXEC|O_NOFOLLOW,0600);if(fd<0)return -1;
  int ok=write(fd,body,strlen(body))==(ssize_t)strlen(body)&&!fsync(fd);close(fd);
  if(!ok||rename(tmp,path)){unlink(tmp);return -1;}
  fd=open(data_dir,O_RDONLY|O_DIRECTORY|O_CLOEXEC);ok=fd>=0&&!fsync(fd);if(fd>=0)close(fd);return ok?0:-1;
}
static cJSON *request(cJSON *q) {
  const char *method = str(q, "method");
  if (!strcmp(method, "status"))
    return status();
  cJSON *j = cJSON_CreateObject();
  cJSON_AddNumberToObject(j, "api_version", 1);
  if(!strcmp(method,"configure")) {
    cJSON *flag=cJSON_GetObjectItemCaseSensitive(q,"wifi_persistence");
    if(!cJSON_IsBool(flag)||wifi_invalid)goto invalid;
    int enabled=cJSON_IsTrue(flag);sample();
    if(enabled!=wifi_persistence) {
      if(enabled) {
        uint64_t c[4];if(counters_read(c))goto save_failed;
        wifi_history.d.last_rx=c[2];wifi_history.d.last_tx=c[3];
        snprintf(wifi_history.d.boot,sizeof(wifi_history.d.boot),"%s",boot);
        snprintf(wifi_history.d.epoch,sizeof(wifi_history.d.epoch),"%s",epoch);
        if(!wifi_history.d.reset_at&&trusted)wifi_history.d.reset_at=time(NULL);
        wifi_history.last_mono=monotime();wifi_history.dirty=1;
      }
      if(wifi_history.dirty&&model_save(&wifi_history,data_dir))goto save_failed;
      if(settings_save(enabled))goto save_failed;
      wifi_persistence=enabled;ram_save();
    }
    cJSON_AddBoolToObject(j,"ok",1);return j;
  }
  if (!strcmp(method, "series")) {
    const char *s = str(q, "source");
    if (strcmp(s, "lte") && strcmp(s, "wifi"))
      goto invalid;
    unsigned id = !strcmp(s, "wifi");
    cJSON_AddStringToObject(j, "source", s);
    cJSON *a = cJSON_AddArrayToObject(j, "points");
    for (unsigned i = 0; i < model.count; i++) {
      struct point *p =
          &model.points[(model.pos + POINTS - model.count + i) % POINTS];
      cJSON *v = cJSON_CreateArray();
      cJSON_AddItemToArray(v, cJSON_CreateNumber(p->time - model.last_mono));
      cJSON_AddItemToArray(
          v, cJSON_CreateNumber(p->dt > 0 ? (double)p->rx[id] / p->dt : 0));
      cJSON_AddItemToArray(
          v, cJSON_CreateNumber(p->dt > 0 ? (double)p->tx[id] / p->dt : 0));
      cJSON_AddItemToArray(a, v);
    }
    return j;
  }
  if (!strcmp(method, "history")) {
    const char *period = str(q, "period");
    const char *source=str(q,"source");
    if(*source&&strcmp(source,"lte")&&strcmp(source,"wifi"))goto invalid;
    struct model *m=!strcmp(source,"wifi")?&wifi_history:&model;
    int month = !strcmp(period, "month");
    if (!month && strcmp(period, "day"))
      goto invalid;
    cJSON_AddStringToObject(j, "source", m==&wifi_history?"wifi":"lte");
    cJSON_AddStringToObject(j, "period", period);
    cJSON *a = cJSON_AddArrayToObject(j, "buckets");
    struct bucket *b = month ? m->d.months : m->d.days;
    unsigned n = month ? MONTHS : DAYS;
    for (unsigned i = 0; i < n; i++)
      if (b[i].key) {
        cJSON *v = cJSON_CreateObject();
        cJSON_AddNumberToObject(v, "date", (double)b[i].key);
        bytes(v, "rx", b[i].rx);
        bytes(v, "tx", b[i].tx);
        cJSON_AddItemToArray(a, v);
      }
    return j;
  }
  if (!strcmp(method, "reset_period")) {
    const char *source=str(q,"source");
    if(*source&&strcmp(source,"lte")&&strcmp(source,"wifi"))goto invalid;
    struct model *m=!strcmp(source,"wifi")?&wifi_history:&model;
    if(m==&wifi_history&&wifi_invalid)goto invalid;
    sample();
    struct durable old = m->d;
    model_reset(m, trusted ? time(NULL) : 0);
    if (model_save(m, data_dir)) {
      m->d = old;
      m->dirty = 1;
      cJSON_AddStringToObject(j, "error", "statistics_save_failed");
    } else {
      ram_save();
      cJSON_AddBoolToObject(j, "ok", 1);
    }
    return j;
  }
  goto invalid;
save_failed:
  cJSON_AddStringToObject(j,"error","statistics_save_failed");return j;
invalid:
  cJSON_AddStringToObject(j, "error", "invalid_statistics_request");
  return j;
}
static void serve(int server) {
  int fd = accept4(server, NULL, NULL, SOCK_CLOEXEC);
  if (fd < 0)
    return;
  struct ucred peer;
  socklen_t len = sizeof(peer);
  if (getsockopt(fd, SOL_SOCKET, SO_PEERCRED, &peer, &len) ||
      (peer.uid != 0 && peer.uid != 405)) {
    close(fd);
    return;
  }
  struct timeval tv = {0, 250000};
  setsockopt(fd, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
  setsockopt(fd, SOL_SOCKET, SO_SNDTIMEO, &tv, sizeof(tv));
  char buf[LIMIT + 1];
  ssize_t n = recv(fd, buf, LIMIT, MSG_TRUNC);
  cJSON *q = n > 0 && n < LIMIT ? (buf[n] = 0, cJSON_Parse(buf)) : NULL;
  cJSON *j = request(q);
  cJSON_Delete(q);
  char *s = cJSON_PrintUnformatted(j);
  if (s && strlen(s) < LIMIT)
    send(fd, s, strlen(s), MSG_NOSIGNAL);
  else
    send(fd, "{\"error\":\"response_limit\"}", 26, MSG_NOSIGNAL);
  free(s);
  cJSON_Delete(j);
  close(fd);
}
static int client(const char *body) {
  int s = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  const char *p = getenv("MICROSTICK_STATISTICS_SOCKET");
  snprintf(a.sun_path, sizeof(a.sun_path), "%s",
           p ? p : "/run/microstick/plugins/statistics/runtime/api.sock");
  struct timeval tv = {2, 0};
  setsockopt(s, SOL_SOCKET, SO_RCVTIMEO, &tv, sizeof(tv));
  if (s < 0 || connect(s, (void *)&a, sizeof(a)) ||
      send(s, body, strlen(body), MSG_NOSIGNAL) < 0) {
    perror("statistics");
    if (s >= 0)
      close(s);
    return 1;
  }
  char b[LIMIT + 1];
  ssize_t n = recv(s, b, LIMIT, 0);
  close(s);
  if (n <= 0)
    return 1;
  b[n] = 0;
  puts(b);
  return 0;
}
int main(int argc, char **argv) {
  if (argc == 3 && !strcmp(argv[1], "--request"))
    return client(argv[2]);
  if (argc != 1)
    return 2;
  const char *d = getenv("MICROSTICK_PLUGIN_DATA"),
             *r = getenv("MICROSTICK_PLUGIN_RUNTIME");
  if (!d || !r || strlen(r) + 10 >= sizeof(socket_name))
    return 2;
  snprintf(data_dir, sizeof(data_dir), "%s", d);
  snprintf(runtime, sizeof(runtime), "%s", r);
  snprintf(socket_name, sizeof(socket_name), "%s/api.sock", r);
  snprintf(ram_path, sizeof(ram_path), "%s/working.bin", r);
  umask(077);
  mkdir(data_dir, 0700);
  chmod(data_dir, 0700);
  mkdir(runtime, 0755);
  chmod(runtime, 0755);
  struct statfs fs;
  if (statfs(runtime, &fs) || fs.f_type != TMPFS_MAGIC) {
    fprintf(stderr, "statistics runtime must be tmpfs\n");
    return 1;
  }
  if (textfile("/proc/sys/kernel/random/boot_id", boot, sizeof(boot)))
    return 1;
  if (counters_open(epoch)) {
    perror("statistics counters");
    return 1;
  }
  uint64_t initial[4];
  if (counters_read(initial)) {
    counters_close();
    return 1;
  }
  model_init(&wifi_history);strcpy(wifi_history.source,"wifi");
  wifi_invalid=model_load(&wifi_history,data_dir)<0;
  char settings[256],config[600];snprintf(config,sizeof(config),"%s/settings.json",data_dir);textfile(config,settings,sizeof(settings));
  cJSON *setting=cJSON_Parse(settings);wifi_persistence=cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(setting,"wifi_persistence"));cJSON_Delete(setting);
  model_init(&model);
  if (model_load(&model, data_dir) < 0) {
    fprintf(stderr, "statistics snapshots invalid; refusing to overwrite\n");
    counters_close();
    return 1;
  }
  ram_load();
  if (!model.last_save)
    model.last_save = monotime();
  if(!wifi_history.last_save)wifi_history.last_save=monotime();
  signal(SIGTERM, stop);
  signal(SIGINT, stop);
  signal(SIGHUP, stop);
  setpriority(PRIO_PROCESS, 0, 10);
  int server = socket(AF_UNIX, SOCK_SEQPACKET | SOCK_CLOEXEC, 0);
  struct sockaddr_un a = {.sun_family = AF_UNIX};
  snprintf(a.sun_path, sizeof(a.sun_path), "%s", socket_name);
  unlink(socket_name);
  if (server < 0 || bind(server, (void *)&a, sizeof(a)) || listen(server, 8)) {
    counters_close();
    return 1;
  }
  chown(socket_name, 0, 405);
  chmod(socket_name, 0660);
  int timer = timerfd_create(CLOCK_MONOTONIC, TFD_CLOEXEC);
  struct itimerspec tick = {.it_interval = {3, 0}, .it_value = {3, 0}};
  timerfd_settime(timer, 0, &tick, NULL);
  int notify = inotify_init1(IN_CLOEXEC | IN_NONBLOCK);
  if (notify >= 0)
    inotify_add_watch(notify, core_run(), IN_CLOSE_WRITE | IN_MOVED_TO);
  sample();
  while (!quitting) {
    struct pollfd p[3] = {
        {server, POLLIN, 0}, {timer, POLLIN, 0}, {notify, POLLIN, 0}};
    int n = poll(p, 3, -1);
    if (n < 0) {
      if (errno == EINTR)
        continue;
      break;
    }
    if (p[1].revents & POLLIN) {
      uint64_t v;
      (void)read(timer, &v, sizeof(v));
      sample();
    }
    if (p[2].revents & POLLIN) {
      char events[8192];
      ssize_t nr;
      int update = 0;
      while ((nr = read(notify, events, sizeof(events))) > 0)
        for (char *at = events; at < events + nr;) {
          struct inotify_event *e = (void *)at;
          if (e->len && (!strcmp(e->name, "lan-mode") ||
                         !strcmp(e->name, "wifi-interface")))
            update = 1;
          at += sizeof(*e) + e->len;
        }
      if (update) {
        uint64_t c[4];
        if (!counters_read(c))
          model_sample(&model, c, boot, epoch, wifi_name, monotime(),
                       time(NULL), trusted);
        source();
      }
    }
    if (p[0].revents & POLLIN)
      serve(server);
  }
  sample();
  if (model.dirty && model_save(&model, data_dir))
    perror("statistics save");
  if(wifi_persistence&&!wifi_invalid&&wifi_history.dirty&&model_save(&wifi_history,data_dir))perror("Wi-Fi statistics save");
  counters_close();
  unlink(socket_name);
  unlink(ram_path);
  close(server);
  close(timer);
  if (notify >= 0)
    close(notify);
  return 0;
}
