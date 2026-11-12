#ifndef STATISTICS_MODEL_H
#define STATISTICS_MODEL_H
#include <stdint.h>
#define DAYS 90
#define MONTHS 24
#define POINTS 300
struct bucket {
  int64_t key;
  uint64_t rx, tx;
};
/* Source-independent disk v1. Separate files keep mobile/Wi-Fi isolated. */
struct durable {
  uint64_t magic, version, generation, crc;
  uint64_t rx, tx, base_rx, base_tx, last_rx, last_tx, undated_rx, undated_tx;
  int64_t reset_at, started_at;
  char boot[40], epoch[80];
  struct bucket days[DAYS], months[MONTHS];
};
struct point {
  double time, dt;
  uint64_t rx[2], tx[2];
};
struct model {
  struct durable d;
  char source[5];
  uint64_t wifi_rx, wifi_tx, wifi_last_rx, wifi_last_tx;
  double last_mono, wifi_started, last_save;
  int dirty;
  uint64_t saved_rx, saved_tx;
  char wifi_session[160];
  struct point points[POINTS];
  unsigned pos, count;
};
uint64_t checksum(const void *, unsigned);
void model_init(struct model *);
void model_sample(struct model *, const uint64_t[4], const char *, const char *,
                  const char *, double, int64_t, int);
int model_save(struct model *, const char *);
int model_load(struct model *, const char *);
int model_due(const struct model *, double);
void model_reset(struct model *, int64_t);
#endif
