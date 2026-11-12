#ifndef STATS_NFT_H
#define STATS_NFT_H
#include <stdint.h>
int counters_open(char epoch[80]);
int counters_read(uint64_t c[4]);
int counters_wifi(const char *iface);
void counters_close(void);
#endif
