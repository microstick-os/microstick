#ifndef MICROSTICK_EVENT_H
#define MICROSTICK_EVENT_H
#define _GNU_SOURCE
#include <cjson/cJSON.h>
#include <gio/gio.h>
#include <glib-unix.h>
#include <stdint.h>
#define EV_LIMIT 32768
const char *ev_run(void);
const char *ev_etc(void);
char *ev_path(const char *base, const char *name);
char *ev_read(const char *base, const char *name);
cJSON *ev_json(const char *base, const char *name);
const char *ev_str(const cJSON *j, const char *key);
double ev_num(const cJSON *j, const char *key);
void ev_set(cJSON *j, const char *key, const char *value);
gboolean ev_write(const char *base, const char *name, const char *value);
gboolean ev_write_json(const char *base, const char *name, cJSON *j);
void ev_remove(const char *base, const char *name);
char *ev_conf(const char *name, const char *key, const char *fallback);
double ev_now(void);
int ev_config_lock(void);
void ev_config_unlock(int fd);
gboolean ev_alive(const char *name);
cJSON *ev_lan(void);
cJSON *ev_addresses(void);
gboolean ev_address(const char *iface, const char *cidr);
char *ev_uplink(void);
int ev_netlink(unsigned groups);
typedef void (*EvChanged)(unsigned type, gpointer data);
void ev_watch_files(EvChanged changed, gpointer data);
void ev_watch_routes(EvChanged changed, gpointer data);
typedef cJSON *(*EvRequest)(cJSON *request, int fd, gpointer data);
int ev_server(const char *name, EvRequest callback, gpointer data);
char *ev_rpc(const char *name, const char *request, int timeout_ms);
int ev_client(int argc, char **argv, const char *socket_name);
void ev_reply(int fd, cJSON *response);
gboolean ev_valid_iface(const char *name);
char *ev_ctrl(const char *directory, const char *iface, const char *command);
void ev_watch_wifi(const char *directory, const char *iface, EvChanged changed,
                   gpointer data);
cJSON *ev_clients(void);
cJSON *ev_bindings(cJSON *clients);
#endif
