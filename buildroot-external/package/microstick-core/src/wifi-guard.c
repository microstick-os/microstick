/* Wi-Fi safety owns its deadlines; HTTP/UI are never required for recovery. */
#include "wifi-guard.h"
#include <arpa/inet.h>
#include <curl/curl.h>
#include <ifaddrs.h>
#include <stdio.h>
#include <string.h>
#include <unistd.h>
static EvChanged changed;
static gboolean candidate, confirmed, online, probing, active, recovery_hold;
static char *identity, *attempt_id, *candidate_hash;
static char connectivity[32] = "idle";
static double association_deadline, confirm_deadline, next_probe;
static unsigned failures, generation;
static guint timer;
static gboolean exists(const char *base, const char *name) {
  char *p = ev_path(base, name);
  gboolean yes = g_file_test(p, G_FILE_TEST_IS_REGULAR);
  g_free(p);
  return yes;
}
char *wifi_mode(void) {
  if (exists(ev_run(), "wifi-candidate.conf"))
    return g_strdup("sta");
  char *mode = ev_conf("network.conf", "WIFI_MODE", "ap");
  if (!strcmp(mode, "sta") &&
      (recovery_hold || exists(ev_etc(), "wifi-recovery.json"))) {
    g_free(mode);
    return g_strdup("ap");
  }
  return mode;
}
char *wifi_config(void) {
  return exists(ev_run(), "wifi-candidate.conf")
             ? ev_read(ev_run(), "wifi-candidate.conf")
             : ev_read(ev_etc(), "wpa_supplicant.conf");
}
gboolean wifi_ready(const char *iface, const char *status) {
  if (!strstr(status, "wpa_state=COMPLETED"))
    return FALSE;
  FILE *f = fopen("/proc/net/route", "r");
  if (!f)
    return FALSE;
  char line[512], name[64];
  unsigned long dst, flags;
  gboolean ready = FALSE;
  while (fgets(line, sizeof(line), f))
    if (sscanf(line, "%63s %lx %*x %lx", name, &dst, &flags) == 3 &&
        !strcmp(name, iface) && !dst && (flags & 1))
      ready = TRUE;
  fclose(f);
  return ready;
}
static void set_ap(void) {
  char *s = ev_read(ev_etc(), "network.conf"),
       **lines = g_strsplit(s, "\n", -1);
  GString *out = g_string_new("");
  gboolean found = FALSE;
  for (int i = 0; lines[i]; i++) {
    if (!*lines[i] && !lines[i + 1])
      break;
    if (g_str_has_prefix(lines[i], "WIFI_MODE=")) {
      g_string_append(out, "WIFI_MODE=ap\n");
      found = TRUE;
    } else
      g_string_append_printf(out, "%s\n", lines[i]);
  }
  if (!found)
    g_string_append(out, "WIFI_MODE=ap\n");
  ev_write(ev_etc(), "network.conf", out->str);
  g_string_free(out, TRUE);
  g_strfreev(lines);
  g_free(s);
}
cJSON *wifi_guard_status(void) {
  cJSON *j = cJSON_CreateObject();
  cJSON_AddStringToObject(j, "connectivity", connectivity);
  cJSON_AddBoolToObject(j, "pending", candidate);
  cJSON_AddStringToObject(j, "id", candidate && attempt_id ? attempt_id : "");
  cJSON_AddNumberToObject(
      j, "remaining",
      candidate ? MAX(0, (confirm_deadline ? confirm_deadline
                                           : association_deadline) -
                             ev_now())
                : 0);
  return j;
}
static void recover(const char *reason) {
  char saved_reason[32];
  g_strlcpy(saved_reason, reason, sizeof(saved_reason));
  reason = saved_reason;
  recovery_hold = TRUE;
  int lock = ev_config_lock();
  cJSON *j = cJSON_CreateObject();
  cJSON_AddStringToObject(j, "reason", reason);
  /* Even if the persistent write fails, this process must still return AP. */
  ev_write_json(ev_etc(), "wifi-recovery.json", j);
  cJSON_Delete(j);
  ev_remove(ev_run(), "wifi-candidate.conf");
  ev_remove(ev_run(), "wifi-stage");
  set_ap();
  ev_config_unlock(lock);
  candidate = FALSE;
  active = FALSE;
  online = FALSE;
  generation++;
  g_strlcpy(connectivity, reason, sizeof(connectivity));
  ev_write(ev_run(), "wifi-state", "failed");
  ev_write(ev_run(), "wifi-error", reason);
  changed(1, NULL);
}
static void commit(void) {
  int lock = ev_config_lock();
  if (lock < 0) {
    recover("wifi_save_failed");
    return;
  }
  char *config = ev_read(ev_run(), "wifi-candidate.conf");
  char *hash = g_compute_checksum_for_string(G_CHECKSUM_SHA256, config, -1);
  gboolean same = candidate_hash && !strcmp(hash, candidate_hash);
  g_free(hash);
  if (!same) {
    ev_config_unlock(lock);
    g_free(config);
    changed(1, NULL);
    return;
  }
  char *previous = ev_read(ev_etc(), "wpa_supplicant.conf");
  if (!ev_write(ev_etc(), "wpa_supplicant.conf", config)) {
    g_free(previous);
    g_free(config);
    ev_config_unlock(lock);
    recover("wifi_save_failed");
    return;
  }
  g_free(config);
  /* Commit mode before removing the runtime override. */
  char *s = ev_read(ev_etc(), "network.conf");
  GRegex *rx = g_regex_new("(?m)^WIFI_MODE=.*$", 0, 0, NULL);
  char *out = g_regex_replace_literal(rx, s, -1, 0, "WIFI_MODE=sta", 0, NULL);
  gboolean ok = out && strstr(out, "WIFI_MODE=sta") &&
                ev_write(ev_etc(), "network.conf", out);
  g_free(out);
  g_regex_unref(rx);
  g_free(s);
  if (!ok) {
    ev_write(ev_etc(), "wpa_supplicant.conf", previous);
    g_free(previous);
    ev_config_unlock(lock);
    recover("wifi_save_failed");
    return;
  }
  g_free(previous);
  candidate = FALSE;
  recovery_hold = FALSE;
  ev_remove(ev_etc(), "wifi-recovery.json");
  ev_remove(ev_run(), "wifi-candidate.conf");
  ev_remove(ev_run(), "wifi-error");
  ev_write(ev_run(), "wifi-state", "connected");
  ev_config_unlock(lock);
  changed(1, NULL);
}
struct probe {
  char *iface;
  unsigned generation;
};
struct response {
  char bytes[512];
  size_t used;
};
static size_t receive_body(char *ptr, size_t size, size_t nmemb, void *data) {
  struct response *r = data;
  size_t n = size * nmemb;
  if (n > sizeof(r->bytes) - 1 - r->used)
    return 0;
  memcpy(r->bytes + r->used, ptr, n);
  r->used += n;
  r->bytes[r->used] = 0;
  return n;
}
static void probe_free(gpointer p) {
  struct probe *v = p;
  g_free(v->iface);
  g_free(v);
}
static void probe_thread(GTask *task, gpointer object, gpointer data,
                         GCancellable *cancel) {
  (void)object;
  (void)cancel;
  struct probe *p = data;
  int result = 0;
  const char *urls[] = {g_getenv("MICROSTICK_CONNECTIVITY_URL1"),
                        g_getenv("MICROSTICK_CONNECTIVITY_URL2")};
  if (!urls[0])
    urls[0] = "http://connectivitycheck.gstatic.com/generate_204";
  if (!urls[1])
    urls[1] = "http://www.msftconnecttest.com/connecttest.txt";
  for (int i = 0; i < 2; i++) {
    CURL *c = curl_easy_init();
    if (!c)
      continue;
    struct response r = {0};
    long code = 0;
    curl_easy_setopt(c, CURLOPT_URL, urls[i]);
    curl_easy_setopt(c, CURLOPT_INTERFACE, p->iface);
    curl_easy_setopt(c, CURLOPT_PROXY, "");
    curl_easy_setopt(c, CURLOPT_FOLLOWLOCATION, 0L);
    curl_easy_setopt(c, CURLOPT_TIMEOUT_MS, 5000L);
    curl_easy_setopt(c, CURLOPT_CONNECTTIMEOUT_MS, 3000L);
    curl_easy_setopt(c, CURLOPT_NOSIGNAL, 1L);
    curl_easy_setopt(c, CURLOPT_IPRESOLVE, CURL_IPRESOLVE_V4);
    curl_easy_setopt(c, CURLOPT_WRITEFUNCTION, receive_body);
    curl_easy_setopt(c, CURLOPT_WRITEDATA, &r);
    CURLcode rc = curl_easy_perform(c);
    curl_easy_getinfo(c, CURLINFO_RESPONSE_CODE, &code);
    curl_easy_cleanup(c);
    g_strchomp(r.bytes);
    if (rc == CURLE_OK &&
        ((!i && code == 204 && !r.used) ||
         (i && code == 200 && !strcmp(r.bytes, "Microsoft Connect Test")))) {
      result = 1;
      break;
    }
    if ((code >= 300 && code < 400) || (code == 200 && r.used))
      result = 2;
  }
  g_task_return_int(task, result);
}
static void probe_done(GObject *object, GAsyncResult *result, gpointer data) {
  (void)object;
  (void)data;
  GTask *task = G_TASK(result);
  struct probe *p = g_task_get_task_data(task);
  int r = g_task_propagate_int(task, NULL);
  if (p->generation != generation)
    return;
  probing = FALSE;
  if (r == 1) {
    online = TRUE;
    failures = 0;
    next_probe = ev_now() + 60;
    g_strlcpy(connectivity, "online", sizeof(connectivity));
    if (candidate && confirmed)
      commit();
  } else {
    online = FALSE;
    failures++;
    next_probe = ev_now() + 5;
    g_strlcpy(connectivity, r == 2 ? "captive_portal" : "no_internet",
              sizeof(connectivity));
    if (failures >= 3)
      recover(connectivity);
  }
  changed(1, NULL);
}
static gboolean wake(gpointer data) {
  (void)data;
  timer = 0;
  changed(1, NULL);
  return G_SOURCE_REMOVE;
}
void wifi_guard_init(EvChanged callback) {
  changed = callback;
  curl_global_init(CURL_GLOBAL_DEFAULT);
  cJSON *hold = ev_json(ev_etc(), "wifi-recovery.json");
  if (*ev_str(hold, "reason")) {
    g_strlcpy(connectivity, ev_str(hold, "reason"), sizeof(connectivity));
    ev_write(ev_run(), "wifi-error", connectivity);
  }
  cJSON_Delete(hold);
  if (exists(ev_run(), "wifi-candidate.conf"))
    ev_write(ev_run(), "wifi-stage", "yes");
}
void wifi_guard_tick(const char *mode, const char *iface, gboolean ready) {
  if (timer) {
    g_source_remove(timer);
    timer = 0;
  }
  if (exists(ev_run(), "wifi-stage")) {
    int lock = ev_config_lock();
    if (exists(ev_run(), "wifi-stage") &&
        exists(ev_run(), "wifi-candidate.conf")) {
      ev_remove(ev_run(), "wifi-stage");
      mode = "sta";
      ready = FALSE;
      changed(1, NULL);
      generation++;
      probing = FALSE;
      candidate = TRUE;
      confirmed = FALSE;
      online = FALSE;
      failures = 0;
      active = TRUE;
      g_free(attempt_id);
      attempt_id = g_uuid_string_random();
      char *config = ev_read(ev_run(), "wifi-candidate.conf");
      g_free(candidate_hash);
      candidate_hash =
          g_compute_checksum_for_string(G_CHECKSUM_SHA256, config, -1);
      g_free(config);
      association_deadline = ev_now() + 45;
      confirm_deadline = 0;
      next_probe = 0;
      g_clear_pointer(&identity, g_free);
      g_strlcpy(connectivity, "connecting", sizeof(connectivity));
      ev_write(ev_run(), "wifi-state", "connecting");
    }
    ev_config_unlock(lock);
  }
  if (strcmp(mode, "sta")) {
    if (active) {
      generation++;
      probing = FALSE;
      active = FALSE;
      online = FALSE;
      candidate = FALSE;
      g_clear_pointer(&identity, g_free);
      g_strlcpy(connectivity, "idle", sizeof(connectivity));
    }
    return;
  }
  if (!active) {
    active = TRUE;
    association_deadline = ev_now() + 45;
    next_probe = 0;
    failures = 0;
  }
  if (!ready) {
    if (identity) {
      generation++;
      probing = FALSE;
      online = FALSE;
      g_clear_pointer(&identity, g_free);
      next_probe = 0;
    }
    if (ev_now() >= association_deadline) {
      recover("connection_failed");
      return;
    }
    timer = g_timeout_add(
        MAX(1, (guint)((association_deadline - ev_now()) * 1000)), wake, NULL);
    return;
  }
  char *status = ev_read(ev_run(), "wifi-status"),
       *current = g_strdup_printf("%s:%s", iface, status);
  /* Only identity fields, not signal/handshake counters, reset validation. */
  char **lines = g_strsplit(status, "\n", -1);
  GString *key = g_string_new(iface);
  for (int i = 0; lines[i]; i++)
    if (g_str_has_prefix(lines[i], "bssid=") ||
        g_str_has_prefix(lines[i], "ip_address="))
      g_string_append(key, lines[i]);
  g_strfreev(lines);
  g_free(current);
  g_free(status);
  if (!identity || strcmp(identity, key->str)) {
    if (identity) {
      generation++;
      probing = FALSE;
      failures = 0;
    }
    g_free(identity);
    identity = g_strdup(key->str);
    next_probe = 0;
    online = FALSE;
  }
  g_string_free(key, TRUE);
  association_deadline = ev_now() + 45;
  if (candidate && !confirm_deadline)
    confirm_deadline = ev_now() + 120;
  if (candidate && ev_now() >= confirm_deadline) {
    recover("wifi_access_unconfirmed");
    return;
  }
  if (!probing && ev_now() >= next_probe) {
    probing = TRUE;
    struct probe *p = g_new0(struct probe, 1);
    p->iface = g_strdup(iface);
    p->generation = generation;
    GTask *task = g_task_new(NULL, NULL, probe_done, NULL);
    g_task_set_task_data(task, p, probe_free);
    g_task_run_in_thread(task, probe_thread);
    g_object_unref(task);
  }
  double due = probing ? ev_now() + 10 : next_probe;
  if (candidate)
    due = MIN(due, confirm_deadline);
  timer = g_timeout_add(MAX(1, (guint)(MAX(.01, due - ev_now()) * 1000)), wake,
                        NULL);
}
cJSON *wifi_confirm(cJSON *q) {
  if (!candidate || !attempt_id || strcmp(ev_str(q, "id"), attempt_id))
    return cJSON_Parse("{\"error\":\"invalid_token\"}");
  char *iface = ev_read(ev_run(), "wifi-interface");
  g_strstrip(iface);
  char *cidr = g_strdup_printf("%s/32", ev_str(q, "local_address"));
  /* HTTP inserts the accepted socket's local address, never a browser field. */
  struct ifaddrs *all = NULL;
  gboolean local = FALSE;
  if (!getifaddrs(&all)) {
    for (struct ifaddrs *a = all; a; a = a->ifa_next) {
      char ip[INET_ADDRSTRLEN];
      if (a->ifa_addr && a->ifa_addr->sa_family == AF_INET &&
          !strcmp(a->ifa_name, iface) &&
          inet_ntop(AF_INET, &((struct sockaddr_in *)a->ifa_addr)->sin_addr, ip,
                    sizeof(ip)) &&
          !strcmp(ip, ev_str(q, "local_address")))
        local = TRUE;
    }
    freeifaddrs(all);
  }
  g_free(cidr);
  g_free(iface);
  if (!local || !confirm_deadline || ev_now() >= confirm_deadline)
    return cJSON_Parse("{\"error\":\"wifi_confirm_interface\"}");
  confirmed = TRUE;
  if (online)
    commit();
  return cJSON_Parse("{\"ok\":true}");
}
