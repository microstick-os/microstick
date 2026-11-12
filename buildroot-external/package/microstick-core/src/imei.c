#define _GNU_SOURCE
/* MSM8916 SMD DIAG: IMEI 550 and the profile-gated, zero-only 2500 guard. */
#include <cjson/cJSON.h>
#include <errno.h>
#include <fcntl.h>
#include <glob.h>
#include <linux/rpmsg.h>
#include <poll.h>
#include <stdint.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/file.h>
#include <sys/ioctl.h>
#include <time.h>
#include <unistd.h>

static int valid_imei(const char *s) {
  if (!s || strlen(s) != 15) return 0;
  int sum = 0, nonzero = 0;
  for (int i = 0; i < 15; i++) {
    if (s[i] < '0' || s[i] > '9') return 0;
    int v = s[i] - '0'; nonzero |= v;
    if (i % 2) { v *= 2; if (v > 9) v -= 9; }
    sum += v;
  }
  return nonzero && sum % 10 == 0;
}
static void encode(const char *s, uint8_t *p) {
  p[0] = 8; p[1] = (uint8_t)(((s[0]-'0') << 4) | 0xa);
  for (int i = 1; i < 15; i += 2)
    p[2+(i-1)/2] = (uint8_t)((s[i]-'0') | ((s[i+1]-'0') << 4));
}
static int decode(const uint8_t *p, char s[16]) {
  if (p[0] != 8 || (p[1]&15) != 0xa) return 0;
  s[0] = (char)('0'+(p[1]>>4));
  for (int i = 1; i < 15; i += 2) {
    s[i] = (char)('0'+(p[2+(i-1)/2]&15));
    s[i+1] = (char)('0'+(p[2+(i-1)/2]>>4));
  }
  s[15] = 0;
  /* Some factory identifiers have a zero check digit; preserve them verbatim. */
  for (int i = 0; i < 15; i++) if (s[i] < '0' || s[i] > '9') return 0;
  return 1;
}
static long millis(void) {
  struct timespec ts; clock_gettime(CLOCK_MONOTONIC, &ts);
  return ts.tv_sec * 1000L + ts.tv_nsec / 1000000;
}
static int endpoint(char path[256]) {
  glob_t g = {0};
  char proc[64] = "", base[512], pattern[600];
  /* remoteproc numbering varies with probe order. Select the modem by hardware name. */
  if (!glob("/sys/class/remoteproc/remoteproc*/name", 0, NULL, &g)) {
    for (size_t i = 0; i < g.gl_pathc; i++) {
      FILE *f = fopen(g.gl_pathv[i], "r"); char name[64] = {0};
      if (f) { if (!fgets(name, sizeof(name), f)) name[0]=0; fclose(f); }
      if (strcmp(name, "4080000.remoteproc\n")) continue;
      char p[256]; snprintf(p, sizeof(p), "%s", g.gl_pathv[i]);
      *strrchr(p, '/')=0; snprintf(proc, sizeof(proc), "%s", strrchr(p, '/')+1);
      break;
    }
  }
  globfree(&g);
  if (!proc[0]) return -1;
  snprintf(base, sizeof(base), "/sys/bus/rpmsg/devices/%s:smd-edge.rpmsg_chrdev.0.0/rpmsg/rpmsg_ctrl*", proc);
  if (glob(base, 0, NULL, &g) || g.gl_pathc != 1) { globfree(&g); return -1; }
  char ctrlpath[256];
  snprintf(ctrlpath, sizeof(ctrlpath), "/dev/%s", strrchr(g.gl_pathv[0], '/')+1);
  globfree(&g);
  snprintf(pattern, sizeof(pattern), "%s/rpmsg[0-9]*/name", base);
  path[0] = 0;
  for (int attempt=0; attempt<2; attempt++) {
    if (!glob(pattern, 0, NULL, &g)) {
      for (size_t i = 0; i < g.gl_pathc; i++) {
        FILE *f = fopen(g.gl_pathv[i], "r");
        char name[64] = {0};
        if (f) { if (!fgets(name, sizeof(name), f)) name[0] = 0; fclose(f); }
        if (strcmp(name, "DIAG\n")) continue;
        char p[512]; snprintf(p, sizeof(p), "%s", g.gl_pathv[i]);
        *strrchr(p, '/') = 0;
        snprintf(path, 256, "/dev/%s", strrchr(p, '/')+1);
        break;
      }
    }
    globfree(&g);
    if (path[0]) break;
    if (attempt == 0) {
      int ctrl = open(ctrlpath, O_RDWR | O_CLOEXEC);
      if (ctrl < 0) return -1;
      struct rpmsg_endpoint_info info = {.name="DIAG", .src=RPMSG_ADDR_ANY, .dst=RPMSG_ADDR_ANY};
      int rc = ioctl(ctrl, RPMSG_CREATE_EPT_IOCTL, &info);
      close(ctrl);
      if (rc) return -1;
    }
  }
  if (!path[0]) return -1;
  long end = millis()+2000;
  do {
    int fd = open(path, O_RDWR | O_NONBLOCK | O_CLOEXEC);
    if (fd >= 0) return fd;
    if (errno != ENOENT && errno != EINVAL) return -1;
    usleep(20000);
  } while (millis() < end);
  return -1;
}
static uint16_t crc(const uint8_t *p, size_t n) {
  uint16_t c = 0xffff;
  for (size_t i = 0; i < n; i++) {
    c ^= p[i];
    for (int j = 0; j < 8; j++) c = (c & 1) ? (c >> 1) ^ 0x8408 : c >> 1;
  }
  return c;
}
/* 1: write marker into an empty item; 0: already valid; -1: preserve other data. */
static int guard_payload(const uint8_t original[128], uint8_t marker[128]) {
  uint8_t zero[128] = {0};
  memset(marker, 0, 128); memcpy(marker, "HiMI_OK", 7);
  if (!memcmp(original, marker, 128)) return 0;
  return memcmp(original, zero, 128) ? -1 : 1;
}
/* Requests on SMD are raw DIAG packets; replies use CRC-protected HDLC. */
static const char *nv(int fd, uint8_t command, uint16_t item, uint8_t data[128]) {
  uint8_t request[133] = {0};
  request[0] = command; request[1] = item & 255; request[2] = item >> 8;
  if (command == 0x27) memcpy(request+3, data, 128);
  if (write(fd, request, sizeof(request)) != sizeof(request)) return "imei_io_failed";
  uint8_t frame[32768]; size_t used = 0; int escaped = 0;
  long end = millis()+5000;
  while (millis() < end) {
    struct pollfd p = {fd, POLLIN, 0};
    int ready = poll(&p, 1, (int)(end-millis()));
    if (ready < 0 && errno == EINTR) continue;
    if (ready <= 0) break;
    uint8_t buf[32768]; ssize_t n = read(fd, buf, sizeof(buf));
    if (n < 0 && (errno == EINTR || errno == EAGAIN)) continue;
    if (n <= 0) return "imei_io_failed";
    for (ssize_t i = 0; i < n; i++) {
      uint8_t v = buf[i];
      if (v == 0x7e) {
        if (used >= 3 && crc(frame, used) == 0xf0b8) {
          if (frame[0] == 0x42 || frame[0] == 0x17) return "imei_locked";
          if (frame[0] == command && used == 135 && frame[1] == (item & 255) && frame[2] == (item >> 8)) {
            if (frame[131] || frame[132]) return "imei_nv_rejected";
            if (command == 0x26) memcpy(data, frame+3, 128);
            return NULL;
          }
          if (frame[0] == 0x13 || frame[0] == 0x14 || frame[0] == 0x15 || frame[0] == 0x18)
            return "imei_nv_rejected";
        }
        used = 0; escaped = 0;
      } else if (v == 0x7d) escaped = 1;
      else {
        if (used == sizeof(frame)) return "imei_invalid_response";
        frame[used++] = escaped ? (v ^ 0x20) : v; escaped = 0;
      }
    }
  }
  return "imei_timeout";
}
int main(int argc, char **argv) {
  if (argc != 2 || (strcmp(argv[1], "read") && strcmp(argv[1], "set") && strcmp(argv[1], "validate") && strcmp(argv[1], "guard"))) return 2;
  int validation = !strcmp(argv[1], "validate");
  int guard = !strcmp(argv[1], "guard");
  int writing = !strcmp(argv[1], "set") || validation;
  const char *error = NULL;
  char desired[16] = "", current[16] = "";
  if (writing) {
    char input[512] = {0}; size_t n = fread(input, 1, sizeof(input)-1, stdin);
    cJSON *j = n < sizeof(input)-1 ? cJSON_Parse(input) : NULL;
    cJSON *v = cJSON_GetObjectItemCaseSensitive(j, "imei");
    if (!cJSON_IsString(v) || !valid_imei(v->valuestring)) error = "invalid_imei";
    else memcpy(desired, v->valuestring, 16);
    cJSON_Delete(j);
  }
  int fd = -1, lock = -1;
  if (!error && !validation) {
    lock = open("/run/microstick/imei.lock", O_CREAT | O_RDWR | O_CLOEXEC | O_NOFOLLOW, 0600);
    if (lock < 0 || flock(lock, LOCK_EX | LOCK_NB)) error = "imei_busy";
  }
  char path[256];
  if (!error && !validation && (fd = endpoint(path)) < 0) error = "imei_unavailable";
  uint8_t original[128], wanted[128], actual[128];
  if (!error && !validation) error = nv(fd, 0x26, guard ? 2500 : 550, original);
  if (!error && !validation && !guard && !decode(original, current)) error = "imei_invalid_response";
  if (!error && writing && !validation) {
    memcpy(wanted, original, sizeof(wanted)); encode(desired, wanted);
    /* A single write, including idempotent saves, followed by a full readback. */
    error = nv(fd, 0x27, 550, wanted);
    if (!error) error = nv(fd, 0x26, 550, actual);
    if (!error && memcmp(wanted, actual, sizeof(actual))) error = "imei_verify_failed";
    if (!error) memcpy(current, desired, sizeof(current));
  }
  if (!error && guard) {
    uint8_t marker[128]; int state = guard_payload(original, marker);
    if (state < 0) error = "modem_guard_nonzero";
    else if (state > 0) {
        error = nv(fd, 0x27, 2500, marker);
        if (!error) error = nv(fd, 0x26, 2500, actual);
        if (!error && memcmp(marker, actual, 128)) error = "modem_guard_verify_failed";
    }
  }
  if (fd >= 0) { ioctl(fd, RPMSG_DESTROY_EPT_IOCTL); close(fd); }
  if (lock >= 0) close(lock);
  cJSON *out = cJSON_CreateObject();
  cJSON_AddBoolToObject(out, "ok", !error);
  cJSON_AddStringToObject(out, "error", error ? error : "");
  if (!error) cJSON_AddStringToObject(out, "current", current);
  char *s = cJSON_PrintUnformatted(out); puts(s); free(s); cJSON_Delete(out);
  return error ? 1 : 0;
}
