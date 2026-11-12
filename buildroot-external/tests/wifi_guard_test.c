#include <assert.h>
#include <sys/stat.h>
static double clock_value = 100;
static double test_now(void) { return clock_value; }
#define ev_now test_now
#include "wifi-guard.c"
#undef ev_now
static void event(unsigned type, gpointer data) {
  (void)type;
  (void)data;
}
static void stage(void) {
  ev_write(ev_run(), "wifi-candidate.conf",
           "ctrl_interface=/run/wpa_supplicant\nnetwork={\n ssid=6e6577\n}\n");
  ev_write(ev_run(), "wifi-stage", "yes");
  wifi_guard_tick("ap", "lo", FALSE);
  assert(candidate);
}
static void outcome(int result, unsigned gen) {
  GTask *task = g_task_new(NULL, NULL, NULL, NULL);
  struct probe *p = g_new0(struct probe, 1);
  p->generation = gen;
  g_task_set_task_data(task, p, probe_free);
  g_task_return_int(task, result);
  probe_done(NULL, G_ASYNC_RESULT(task), NULL);
  g_object_unref(task);
}
int main(int argc, char **argv) {
  assert(argc >= 2);
  changed = event;
  if (!strcmp(argv[1], "probe")) {
    struct probe p = {.iface = "lo"};
    GTask *task = g_task_new(NULL, NULL, NULL, NULL);
    probe_thread(task, NULL, &p, NULL);
    int result = g_task_propagate_int(task, NULL);
    printf("%d\n", result);
    g_object_unref(task);
    return 0;
  }
  ev_write(ev_etc(), "network.conf", "WIFI_MODE=ap\nUPLINK_MODE=auto\n");
  ev_write(ev_etc(), "wpa_supplicant.conf", "old-safe-profile\n");
  stage();
  clock_value = 146;
  wifi_guard_tick("sta", "lo", FALSE);
  assert(!candidate && exists(ev_etc(), "wifi-recovery.json"));
  char *old = ev_read(ev_etc(), "wpa_supplicant.conf");
  assert(!strcmp(old, "old-safe-profile\n"));
  g_free(old);
  /* An auto-policy selecting STA after reboot cannot bypass the persisted hold.
   */
  ev_write(ev_etc(), "network.conf", "WIFI_MODE=sta\nUPLINK_MODE=auto\n");
  char *mode = wifi_mode();
  assert(!strcmp(mode, "ap"));
  g_free(mode);
  stage();
  confirm_deadline = clock_value + 120;
  online = TRUE;
  ev_write(ev_run(), "wifi-interface", "lo");
  cJSON *q = cJSON_CreateObject();
  cJSON_AddStringToObject(q, "id", attempt_id);
  cJSON_AddStringToObject(q, "local_address", "192.0.2.99");
  cJSON *r = wifi_confirm(q);
  assert(*ev_str(r, "error"));
  cJSON_Delete(r);
  ev_set(q, "local_address", "127.0.0.1");
  r = wifi_confirm(q);
  assert(cJSON_IsTrue(cJSON_GetObjectItemCaseSensitive(r, "ok")));
  cJSON_Delete(r);
  cJSON_Delete(q);
  assert(!candidate && !exists(ev_etc(), "wifi-recovery.json"));
  old = ev_read(ev_etc(), "wpa_supplicant.conf");
  assert(strstr(old, "ssid=6e6577"));
  g_free(old);
  stage();
  /* A later candidate cannot be committed by an earlier attempt's callback. */
  ev_write(ev_run(), "wifi-candidate.conf", "not-yet-validated-next-profile");
  online = TRUE;
  commit();
  assert(candidate);
  old = ev_read(ev_etc(), "wpa_supplicant.conf");
  assert(strstr(old, "ssid=6e6577"));
  g_free(old);
  stage();
  confirm_deadline = clock_value + 120;
  association_deadline = clock_value + 500;
  clock_value += 121;
  /* Deadline runs even without a UI process. */
  next_probe = clock_value + 999;
  wifi_guard_tick("sta", "lo", TRUE);
  assert(!candidate);
  stage();
  unsigned previous = generation;
  generation++;
  outcome(1, previous);
  assert(!online);
  failures = 0;
  outcome(2, generation);
  outcome(2, generation);
  outcome(2, generation);
  assert(!candidate && !strcmp(connectivity, "captive_portal"));
  stage();
  ev_remove(ev_etc(), "wifi-recovery.json");
  ev_write(ev_etc(), "network.conf", "WIFI_MODE=sta\n");
  assert(!chmod(ev_etc(), 0500));
  recover("no_internet");
  mode = wifi_mode();
  assert(!strcmp(mode, "ap"));
  g_free(mode);
  assert(!chmod(ev_etc(), 0700));
  puts("PASS: deadline, persisted hold, safe profile preservation, "
       "actual-interface confirmation, commit, stale response, captive "
       "recovery");
  return 0;
}
