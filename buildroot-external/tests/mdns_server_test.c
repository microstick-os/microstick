/* Exercise the exact target Avahi core under QEMU, inside an isolated network.
 */
#include <avahi-common/malloc.h>
#include <avahi-common/simple-watch.h>
#include <avahi-core/core.h>
#include <stdio.h>
static void state(AvahiServer *server, AvahiServerState status, void *data) {
  (void)server;
  (void)data;
  if (status == AVAHI_SERVER_RUNNING) {
    puts("ready");
    fflush(stdout);
  }
}
int main(void) {
  AvahiServerConfig config;
  avahi_server_config_init(&config);
  avahi_free(config.host_name);
  config.host_name = avahi_strdup("microstick");
  config.use_ipv6 = 0;
  config.publish_hinfo = 0;
  config.publish_workstation = 0;
  config.allow_interfaces = avahi_string_list_new("ms-sta", NULL);
  AvahiSimplePoll *poll = avahi_simple_poll_new();
  int error;
  AvahiServer *server = avahi_server_new(avahi_simple_poll_get(poll), &config,
                                         state, NULL, &error);
  avahi_server_config_free(&config);
  if (!server)
    return 1;
  avahi_simple_poll_loop(poll);
  avahi_server_free(server);
  avahi_simple_poll_free(poll);
  return 0;
}
