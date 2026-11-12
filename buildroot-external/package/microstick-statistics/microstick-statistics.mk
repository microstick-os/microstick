MICROSTICK_STATISTICS_VERSION = 1
MICROSTICK_STATISTICS_SITE = $(BR2_EXTERNAL_MICROSTICK_PATH)/package/microstick-statistics/src
MICROSTICK_STATISTICS_SITE_METHOD = local
MICROSTICK_STATISTICS_DEPENDENCIES = cjson libmnl libnftnl
MICROSTICK_STATISTICS_LICENSE = GPL-2.0+
define MICROSTICK_STATISTICS_BUILD_CMDS
 $(TARGET_CC) $(TARGET_CFLAGS) -Wall -Wextra -Werror $(@D)/daemon.c $(@D)/model.c $(@D)/nft.c -o $(@D)/microstick-statistics $(TARGET_LDFLAGS) -lcjson -lmnl -lnftnl
endef
define MICROSTICK_STATISTICS_INSTALL_TARGET_CMDS
 $(INSTALL) -D -m 0755 $(@D)/microstick-statistics $(TARGET_DIR)/usr/share/microstick/default-plugins/statistics/bin/microstick-statistics
endef
$(eval $(generic-package))
