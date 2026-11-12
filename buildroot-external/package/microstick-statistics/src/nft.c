/* SPDX-License-Identifier: GPL-2.0-or-later */
#define _GNU_SOURCE
#include "nft.h"
#include <errno.h>
#include <libmnl/libmnl.h>
#include <libnftnl/chain.h>
#include <libnftnl/expr.h>
#include <libnftnl/rule.h>
#include <libnftnl/table.h>
#include <linux/netfilter.h>
#include <linux/netfilter/nf_tables.h>
#include <linux/netfilter/nfnetlink.h>
#include <poll.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>
#include <sys/socket.h>
#include <unistd.h>
#define TABLE "microstick_statistics"
static struct mnl_socket *nl;
static uint32_t seq;
static const char *chains[] = {"lte_rx", "lte_tx", "wifi_rx", "wifi_tx"};
static char wifi[32];
struct batch {
  char data[16384];
  size_t len;
  unsigned acks;
};
static struct nlmsghdr *header(struct batch *b, uint16_t type, uint16_t flags) {
  struct nlmsghdr *h = nftnl_nlmsg_build_hdr(
      b->data + b->len, type, NFPROTO_IPV4, flags | NLM_F_ACK, ++seq);
  b->acks++;
  return h;
}
static void finish_msg(struct batch *b, struct nlmsghdr *h) {
  b->len += NLMSG_ALIGN(h->nlmsg_len);
}
static int receive(unsigned acks, int dump,
                   int (*cb)(const struct nlmsghdr *, void *), void *ctx) {
  char buf[32768];
  while (acks || dump) {
    struct pollfd p = {mnl_socket_get_fd(nl), POLLIN, 0};
    if (poll(&p, 1, 1000) <= 0) {
      errno = ETIMEDOUT;
      return -1;
    }
    int n = mnl_socket_recvfrom(nl, buf, sizeof(buf));
    if (n <= 0)
      return -1;
    for (struct nlmsghdr *h = (void *)buf; NLMSG_OK(h, n);
         h = NLMSG_NEXT(h, n)) {
      if (h->nlmsg_type == NLMSG_ERROR) {
        struct nlmsgerr *e = NLMSG_DATA(h);
        if (e->error) {
          errno = -e->error;
          return -1;
        }
        if (acks)
          acks--;
      } else if (h->nlmsg_type == NLMSG_DONE)
        dump = 0;
      else if (cb && cb(h, ctx) < 0) {
        errno = EPROTO;
        return -1;
      }
    }
  }
  return 0;
}
static int commit(struct batch *b) {
  struct nlmsghdr *h = nftnl_batch_end(b->data + b->len, ++seq);
  b->len += NLMSG_ALIGN(h->nlmsg_len);
  if (mnl_socket_sendto(nl, b->data, b->len) < 0)
    return -1;
  return receive(b->acks, 0, NULL, NULL);
}
static void begin(struct batch *b) {
  memset(b, 0, sizeof(*b));
  struct nlmsghdr *h = nftnl_batch_begin(b->data, ++seq);
  b->len = NLMSG_ALIGN(h->nlmsg_len);
}
static int table_cb(const struct nlmsghdr *h, void *v) {
  struct nftnl_table *t = nftnl_table_alloc();
  if (!t)
    return -1;
  int ret = nftnl_table_nlmsg_parse(h, t);
  if (!ret) {
    uint32_t n = 0;
    const char *s = nftnl_table_get_data(t, NFTNL_TABLE_USERDATA, &n);
    if (s && n && n <= 80 && s[n - 1] == 0)
      memcpy(v, s, n);
  }
  nftnl_table_free(t);
  return ret;
}
static int get_epoch(char out[80]) {
  char buf[512];
  struct nftnl_table *t = nftnl_table_alloc();
  if (!t)
    return -1;
  nftnl_table_set_str(t, NFTNL_TABLE_NAME, TABLE);
  struct nlmsghdr *h = nftnl_nlmsg_build_hdr(buf, NFT_MSG_GETTABLE,
                                             NFPROTO_IPV4, NLM_F_ACK, ++seq);
  nftnl_table_nlmsg_build_payload(h, t);
  nftnl_table_free(t);
  if (mnl_socket_sendto(nl, h, h->nlmsg_len) < 0)
    return -1;
  return receive(1, 0, table_cb, out);
}
static void rule(struct batch *b, unsigned id, const char *iface, int prefix) {
  struct nftnl_rule *r = nftnl_rule_alloc();
  if (!r)
    abort();
  nftnl_rule_set_str(r, NFTNL_RULE_TABLE, TABLE);
  nftnl_rule_set_str(r, NFTNL_RULE_CHAIN, chains[id]);
  nftnl_rule_set_u32(r, NFTNL_RULE_FAMILY, NFPROTO_IPV4);
  struct nftnl_expr *e = nftnl_expr_alloc("meta");
  if (!e)
    abort();
  nftnl_expr_set_u32(e, NFTNL_EXPR_META_KEY,
                     id % 2 ? NFT_META_OIFNAME : NFT_META_IIFNAME);
  nftnl_expr_set_u32(e, NFTNL_EXPR_META_DREG, NFT_REG_1);
  nftnl_rule_add_expr(r, e);
  e = nftnl_expr_alloc("cmp");
  if (!e)
    abort();
  nftnl_expr_set_u32(e, NFTNL_EXPR_CMP_SREG, NFT_REG_1);
  nftnl_expr_set_u32(e, NFTNL_EXPR_CMP_OP, NFT_CMP_EQ);
  nftnl_expr_set(e, NFTNL_EXPR_CMP_DATA, iface,
                 (uint32_t)strlen(iface) + (prefix ? 0 : 1));
  nftnl_rule_add_expr(r, e);
  if(id<2) e=nftnl_expr_alloc("counter");
  else {
    e=nftnl_expr_alloc("immediate");
    if(!e)abort();
    nftnl_expr_set_u32(e,NFTNL_EXPR_IMM_DREG,NFT_REG_VERDICT);
    nftnl_expr_set_u32(e,NFTNL_EXPR_IMM_VERDICT,NFT_JUMP);
    nftnl_expr_set_str(e,NFTNL_EXPR_IMM_CHAIN,id==2?"wifi_rx_count":"wifi_tx_count");
  }
  if (!e) abort();
  nftnl_rule_add_expr(r, e);
  struct nlmsghdr *h = header(b, NFT_MSG_NEWRULE, NLM_F_CREATE | NLM_F_APPEND);
  nftnl_rule_nlmsg_build_payload(h, r);
  finish_msg(b, h);
  nftnl_rule_free(r);
}
int counters_open(char epoch[80]) {
  nl = mnl_socket_open(NETLINK_NETFILTER);
  if (!nl || mnl_socket_bind(nl, 0, MNL_SOCKET_AUTOPID) < 0)
    return -1;
  wifi[0] = 0;
  int ret = get_epoch(epoch);
  if (!ret) {
    if (strncmp(epoch, "microstick-statistics-v2:", 24)) {
      errno = EEXIST;
      return -1;
    }
    return 0;
  }
  if (errno != ENOENT)
    return -1;
  char uuid[48] = {0};
  FILE *f = fopen("/proc/sys/kernel/random/uuid", "r");
  if (!f)
    return -1;
  if (!fgets(uuid, sizeof(uuid), f)) {
    fclose(f);
    return -1;
  }
  fclose(f);
  uuid[strcspn(uuid, "\n")] = 0;
  snprintf(epoch, 80, "microstick-statistics-v2:%s", uuid);
  struct batch b;
  begin(&b);
  struct nftnl_table *t = nftnl_table_alloc();
  if (!t)
    abort();
  nftnl_table_set_str(t, NFTNL_TABLE_NAME, TABLE);
  nftnl_table_set_data(t, NFTNL_TABLE_USERDATA, epoch,
                       (uint32_t)strlen(epoch) + 1);
  struct nlmsghdr *h = header(&b, NFT_MSG_NEWTABLE, NLM_F_CREATE | NLM_F_EXCL);
  nftnl_table_nlmsg_build_payload(h, t);
  finish_msg(&b, h);
  nftnl_table_free(t);
  for (unsigned i = 0; i < 4; i++) {
    struct nftnl_chain *c = nftnl_chain_alloc();
    if (!c)
      abort();
    nftnl_chain_set_str(c, NFTNL_CHAIN_TABLE, TABLE);
    nftnl_chain_set_str(c, NFTNL_CHAIN_NAME, chains[i]);
    nftnl_chain_set_str(c, NFTNL_CHAIN_TYPE, "filter");
    nftnl_chain_set_u32(c, NFTNL_CHAIN_HOOKNUM,
                        i % 2 ? NF_INET_POST_ROUTING : NF_INET_PRE_ROUTING);
    nftnl_chain_set_s32(c, NFTNL_CHAIN_PRIO, i % 2 ? 150 : -150);
    nftnl_chain_set_u32(c, NFTNL_CHAIN_POLICY, NF_ACCEPT);
    h = header(&b, NFT_MSG_NEWCHAIN, NLM_F_CREATE);
    nftnl_chain_nlmsg_build_payload(h, c);
    finish_msg(&b, h);
    nftnl_chain_free(c);
  }
  for(unsigned i=2;i<4;i++) {
    const char *name=i==2?"wifi_rx_count":"wifi_tx_count";
    struct nftnl_chain *c=nftnl_chain_alloc();if(!c)abort();
    nftnl_chain_set_str(c,NFTNL_CHAIN_TABLE,TABLE);nftnl_chain_set_str(c,NFTNL_CHAIN_NAME,name);
    h=header(&b,NFT_MSG_NEWCHAIN,NLM_F_CREATE);nftnl_chain_nlmsg_build_payload(h,c);finish_msg(&b,h);nftnl_chain_free(c);
    struct nftnl_rule *r=nftnl_rule_alloc();if(!r)abort();
    nftnl_rule_set_str(r,NFTNL_RULE_TABLE,TABLE);nftnl_rule_set_str(r,NFTNL_RULE_CHAIN,name);
    struct nftnl_expr *e=nftnl_expr_alloc("counter");if(!e)abort();nftnl_rule_add_expr(r,e);
    h=header(&b,NFT_MSG_NEWRULE,NLM_F_CREATE|NLM_F_APPEND);nftnl_rule_nlmsg_build_payload(h,r);finish_msg(&b,h);nftnl_rule_free(r);
  }
  rule(&b, 0, "wwan", 1);
  rule(&b, 1, "wwan", 1);
  return commit(&b);
}
struct reading {
  uint64_t *c;
  unsigned found;
};
static int rule_cb(const struct nlmsghdr *h, void *ctx) {
  struct reading *v = ctx;
  struct nftnl_rule *r = nftnl_rule_alloc();
  if (!r)
    return -1;
  if (nftnl_rule_nlmsg_parse(h, r) < 0) {
    nftnl_rule_free(r);
    return -1;
  }
  const char *name = nftnl_rule_get_str(r, NFTNL_RULE_CHAIN);
  unsigned id = 4;
  for (unsigned i = 0; i < 4; i++)
    if (name && !strcmp(name, chains[i]))
      id = i;
  if(name&&!strcmp(name,"wifi_rx_count"))id=2;
  if(name&&!strcmp(name,"wifi_tx_count"))id=3;
  if (id < 4) {
    struct nftnl_expr_iter *it = nftnl_expr_iter_create(r);
    if (!it) {
      nftnl_rule_free(r);
      return -1;
    }
    struct nftnl_expr *e;
    while ((e = nftnl_expr_iter_next(it))) {
      const char *type = nftnl_expr_get_str(e, NFTNL_EXPR_NAME);
      if (!strcmp(type, "counter")) {
        v->c[id] += nftnl_expr_get_u64(e, NFTNL_EXPR_CTR_BYTES);
        v->found |= 1U << id;
      }
      if (id == 2 && !strcmp(type, "cmp")) {
        uint32_t n = 0;
        const char *s = nftnl_expr_get(e, NFTNL_EXPR_CMP_DATA, &n);
        if (s && n > 0 && n <= sizeof(wifi) && s[n - 1] == 0)
          memcpy(wifi, s, n);
      }
    }
    nftnl_expr_iter_destroy(it);
  }
  nftnl_rule_free(r);
  return 0;
}
int counters_read(uint64_t c[4]) {
  memset(c, 0, 4 * sizeof(*c));
  char buf[512];
  struct nftnl_rule *r = nftnl_rule_alloc();
  if (!r)
    return -1;
  nftnl_rule_set_str(r, NFTNL_RULE_TABLE, TABLE);
  struct nlmsghdr *h = nftnl_nlmsg_build_hdr(buf, NFT_MSG_GETRULE, NFPROTO_IPV4,
                                             NLM_F_DUMP, ++seq);
  nftnl_rule_nlmsg_build_payload(h, r);
  nftnl_rule_free(r);
  struct reading v = {.c = c};
  if (mnl_socket_sendto(nl, h, h->nlmsg_len) < 0 ||
      receive(0, 1, rule_cb, &v) < 0)
    return -1;
  return (v.found & 3) == 3 ? 0 : -1;
}
int counters_wifi(const char *iface) {
  if (!strcmp(wifi, iface))
    return 0;
  struct batch b;
  begin(&b);
  for (unsigned i = 2; i < 4; i++) {
    struct nftnl_rule *r = nftnl_rule_alloc();
    if (!r)
      abort();
    nftnl_rule_set_str(r, NFTNL_RULE_TABLE, TABLE);
    nftnl_rule_set_str(r, NFTNL_RULE_CHAIN, chains[i]);
    struct nlmsghdr *h = header(&b, NFT_MSG_DELRULE, 0);
    nftnl_rule_nlmsg_build_payload(h, r);
    finish_msg(&b, h);
    nftnl_rule_free(r);
    if (*iface)
      rule(&b, i, iface, 0);
  }
  if (commit(&b) < 0)
    return -1;
  snprintf(wifi, sizeof(wifi), "%s", iface);
  return 1;
}
void counters_close(void) {
  if (!nl)
    return;
  struct batch b;
  begin(&b);
  struct nftnl_table *t = nftnl_table_alloc();
  if (t) {
    nftnl_table_set_str(t, NFTNL_TABLE_NAME, TABLE);
    struct nlmsghdr *h = header(&b, NFT_MSG_DELTABLE, 0);
    nftnl_table_nlmsg_build_payload(h, t);
    finish_msg(&b, h);
    nftnl_table_free(t);
    (void)commit(&b);
  }
  mnl_socket_close(nl);
  nl = NULL;
}
