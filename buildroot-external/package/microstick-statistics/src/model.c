#define _GNU_SOURCE
#include "model.h"
#include <errno.h>
#include <fcntl.h>
#include <stdio.h>
#include <string.h>
#include <sys/stat.h>
#include <time.h>
#include <unistd.h>
#define MAGIC UINT64_C(0x4d53544154533131)
uint64_t checksum(const void *v, unsigned n) {
  const unsigned char *p = v;
  uint64_t h = UINT64_C(14695981039346656037);
  while (n--) {
    h ^= *p++;
    h *= UINT64_C(1099511628211);
  }
  return h;
}
void model_init(struct model *m) {
  memset(m, 0, sizeof(*m));
  strcpy(m->source,"lte");
  m->d.magic = MAGIC;
  m->d.version = 1;
}
static void add(struct bucket *b, unsigned n, int64_t key, uint64_t rx,
                uint64_t tx) {
  unsigned at = n;
  for (unsigned i = 0; i < n; i++)
    if (b[i].key == key) {
      at = i;
      break;
    }
  if (at == n) {
    at = 0;
    for (unsigned i = 1; i < n; i++)
      if (b[i].key < b[at].key)
        at = i;
    /* Do not evict recent records after a backwards wall-clock jump. */
    if (b[at].key > key)
      return;
    b[at] = (struct bucket){.key = key};
  }
  b[at].rx += rx;
  b[at].tx += tx;
}
void model_sample(struct model *m, const uint64_t c[4], const char *boot,
                  const char *epoch, const char *wifi, double mono,
                  int64_t wall, int trusted) {
  int same = !strcmp(m->d.boot, boot) && !strcmp(m->d.epoch, epoch);
  uint64_t rx = same && c[0] >= m->d.last_rx ? c[0] - m->d.last_rx : c[0];
  uint64_t tx = same && c[1] >= m->d.last_tx ? c[1] - m->d.last_tx : c[1];
  m->d.last_rx = c[0];
  m->d.last_tx = c[1];
  snprintf(m->d.boot, sizeof(m->d.boot), "%s", boot);
  snprintf(m->d.epoch, sizeof(m->d.epoch), "%s", epoch);
  m->d.rx += rx;
  m->d.tx += tx;
  uint64_t wrx = same && c[2] >= m->wifi_last_rx ? c[2] - m->wifi_last_rx : c[2];
  uint64_t wtx = same && c[3] >= m->wifi_last_tx ? c[3] - m->wifi_last_tx : c[3];
  int fresh = strcmp(m->wifi_session, wifi) != 0;
  if (fresh) {
    m->wifi_rx = m->wifi_tx = 0;
    m->wifi_started = mono;
    snprintf(m->wifi_session, sizeof(m->wifi_session), "%s", wifi);
  }
  m->wifi_last_rx = c[2];
  m->wifi_last_tx = c[3];
  if (*wifi) {
    m->wifi_rx += wrx;
    m->wifi_tx += wtx;
  } else
    wrx = wtx = 0;
  if (trusted) {
    time_t w = wall;
    struct tm t;
    localtime_r(&w, &t);
    int64_t day = (t.tm_year + 1900) * 10000 + (t.tm_mon + 1) * 100 + t.tm_mday;
    if (rx || tx) {
      add(m->d.days, DAYS, day, rx, tx);
      add(m->d.months, MONTHS, day / 100, rx, tx);
    }
    if (!m->d.started_at) {
      m->d.started_at = wall;
      m->dirty = 1;
    }
    if (!m->d.reset_at) {
      m->d.reset_at = wall;
      m->dirty = 1;
    }
  } else {
    m->d.undated_rx += rx;
    m->d.undated_tx += tx;
  }
  double dt = m->last_mono ? mono - m->last_mono : 0;
  if (dt > 0) {
    struct point *p = &m->points[m->pos];
    *p = (struct point){.time = mono, .dt = dt};
    /* A new process/session has no elapsed interval for earlier kernel bytes.
     */
    if (same) {
      p->rx[0] = rx;
      p->tx[0] = tx;
    }
    if (!fresh) {
      p->rx[1] = wrx;
      p->tx[1] = wtx;
    }
    m->pos = (m->pos + 1) % POINTS;
    if (m->count < POINTS)
      m->count++;
  }
  m->last_mono = mono;
  if (rx || tx || !same)
    m->dirty = 1;
}
int model_due(const struct model *m, double now) {
  return m->dirty && now - m->last_save >= 60 &&
         (now - m->last_save >= 300 ||
          m->d.rx - m->saved_rx + m->d.tx - m->saved_tx >= UINT64_C(67108864));
}
int model_save(struct model *m, const char *dir) {
  struct durable d = m->d;
  d.generation++;
  d.crc = 0;
  d.crc = checksum(&d, sizeof(d));
  char name[512];
  snprintf(name, sizeof(name), "%s/%s-%u.bin", dir, m->source,
           (unsigned)(d.generation % 2));
  int fd =
      open(name, O_WRONLY | O_CREAT | O_TRUNC | O_NOFOLLOW | O_CLOEXEC, 0600);
  if (fd < 0)
    return -1;
  const char *p = (const char *)&d;
  size_t left = sizeof(d);
  int ok = 1;
  while (left) {
    ssize_t n = write(fd, p, left);
    if (n < 0 && errno == EINTR)
      continue;
    if (n <= 0) {
      ok = 0;
      break;
    }
    left -= (size_t)n;
    p += n;
  }
  if (ok && fsync(fd))
    ok = 0;
  close(fd);
  int df = open(dir, O_RDONLY | O_DIRECTORY | O_CLOEXEC);
  if (df < 0)
    ok = 0;
  else {
    if (fsync(df))
      ok = 0;
    close(df);
  }
  if (!ok)
    return -1;
  m->d = d;
  m->dirty = 0;
  m->last_save = m->last_mono;
  m->saved_rx = d.rx;
  m->saved_tx = d.tx;
  return 0;
}
int model_load(struct model *m, const char *dir) {
  int found = 0, seen = 0;
  for (unsigned i = 0; i < 2; i++) {
    char p[512];
    snprintf(p, sizeof(p), "%s/%s-%u.bin", dir, m->source, i);
    int fd = open(p, O_RDONLY | O_NOFOLLOW | O_CLOEXEC);
    if (fd < 0)
      continue;
    seen = 1;
    struct durable d;
    size_t used = 0;
    while (used < sizeof(d)) {
      ssize_t n = read(fd, (char *)&d + used, sizeof(d) - used);
      if (n < 0 && errno == EINTR)
        continue;
      if (n <= 0)
        break;
      used += (size_t)n;
    }
    char extra;
    ssize_t tail = read(fd, &extra, 1);
    close(fd);
    if (used != sizeof(d) || tail != 0 || d.magic != MAGIC || d.version != 1 ||
        !memchr(d.boot, 0, sizeof(d.boot)) ||
        !memchr(d.epoch, 0, sizeof(d.epoch)))
      continue;
    uint64_t crc = d.crc;
    d.crc = 0;
    if (crc != checksum(&d, sizeof(d)))
      continue;
    d.crc = crc;
    if (!found || d.generation > m->d.generation) {
      m->d = d;
      found = 1;
    }
  }
  m->saved_rx = m->d.rx;
  m->saved_tx = m->d.tx;
  return found ? 1 : seen ? -1 : 0;
}
void model_reset(struct model *m, int64_t wall) {
  m->d.base_rx = m->d.rx;
  m->d.base_tx = m->d.tx;
  m->d.reset_at = wall;
  m->dirty = 1;
}
