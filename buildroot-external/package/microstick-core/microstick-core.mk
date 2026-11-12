MICROSTICK_CORE_VERSION = 1
MICROSTICK_CORE_SITE = $(BR2_EXTERNAL_MICROSTICK_PATH)/package/microstick-core/src
MICROSTICK_CORE_SITE_METHOD = local
MICROSTICK_CORE_DEPENDENCIES = cjson libmicrohttpd openssl modem-manager host-pkgconf iptables libmnl libnftnl libcurl avahi
MICROSTICK_CORE_LICENSE = MIT

define MICROSTICK_CORE_BUILD_CMDS
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror -DWIFI_TOOL `$(PKG_CONFIG_HOST_BINARY) --cflags glib-2.0` $(@D)/ssid.c -o $(@D)/microstick-wifi $(TARGET_LDFLAGS) -lcjson -lcrypto `$(PKG_CONFIG_HOST_BINARY) --libs glib-2.0`
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror `$(PKG_CONFIG_HOST_BINARY) --cflags gio-unix-2.0` $(@D)/event.c $(@D)/clients.c $(@D)/routerd.c -o $(@D)/microstick-routerd $(TARGET_LDFLAGS) `$(PKG_CONFIG_HOST_BINARY) --libs gio-unix-2.0` -lcjson -lip4tc
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror `$(PKG_CONFIG_HOST_BINARY) --cflags mm-glib gio-unix-2.0` $(@D)/event.c $(@D)/ssid.c $(@D)/wifi-guard.c $(@D)/discovery.c $(@D)/apn.c $(@D)/state.c $(@D)/networkd.c -o $(@D)/microstick-networkd $(TARGET_LDFLAGS) `$(PKG_CONFIG_HOST_BINARY) --libs mm-glib gio-unix-2.0` -lcjson -lmnl -lnftnl -lcurl
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror $(@D)/plugin-watch.c -o $(@D)/microstick-plugin-watch $(TARGET_LDFLAGS)
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror $(@D)/imei.c -o $(@D)/microstick-imei-nv $(TARGET_LDFLAGS) -lcjson
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror `$(PKG_CONFIG_HOST_BINARY) --cflags mm-glib` $(@D)/modem.c $(@D)/apn.c -o $(@D)/microstick-modem $(TARGET_LDFLAGS) `$(PKG_CONFIG_HOST_BINARY) --libs mm-glib` -lcjson
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror $(@D)/service.c -o $(@D)/microstick-core $(TARGET_LDFLAGS) -lcjson
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror -DWEB $(@D)/service.c -o $(@D)/microstick-web $(TARGET_LDFLAGS) -lcjson -lcrypto -lmicrohttpd
endef

define MICROSTICK_CORE_INSTALL_TARGET_CMDS
 $(INSTALL) -D -m 0755 $(@D)/microstick-wifi $(TARGET_DIR)/usr/libexec/microstick-wifi
 $(INSTALL) -D -m 0755 $(@D)/microstick-routerd $(TARGET_DIR)/usr/libexec/microstick-routerd
 $(INSTALL) -D -m 0755 $(@D)/microstick-networkd $(TARGET_DIR)/usr/libexec/microstick-networkd
 rm -f $(TARGET_DIR)/usr/libexec/microstick-snapshot
 $(INSTALL) -D -m 0755 $(@D)/microstick-plugin-watch $(TARGET_DIR)/usr/libexec/microstick-plugin-watch
 $(INSTALL) -D -m 0755 $(@D)/microstick-imei-nv $(TARGET_DIR)/usr/libexec/microstick-imei-nv
 $(INSTALL) -D -m 0755 $(@D)/microstick-modem $(TARGET_DIR)/usr/libexec/microstick-modem
 $(INSTALL) -D -m 0755 $(@D)/microstick-core $(TARGET_DIR)/usr/libexec/microstick-core
 $(INSTALL) -D -m 0755 $(@D)/microstick-web $(TARGET_DIR)/usr/share/microstick/default-plugins/web/bin/microstick-web
 mkdir -p $(TARGET_DIR)/usr/share/microstick/default-plugins/web/lib
 cp -a $(STAGING_DIR)/usr/lib/libmicrohttpd.so* $(TARGET_DIR)/usr/share/microstick/default-plugins/web/lib/
 rm -f $(TARGET_DIR)/usr/lib/libmicrohttpd.so*
endef
$(eval $(generic-package))
