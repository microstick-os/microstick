MICROSTICK_REGDB_VERSION = 2023.09.01
MICROSTICK_REGDB_SOURCE = wireless-regdb-$(MICROSTICK_REGDB_VERSION).tar.xz
MICROSTICK_REGDB_SITE = https://cdn.kernel.org/pub/software/network/wireless-regdb
MICROSTICK_REGDB_LICENSE = ISC
MICROSTICK_REGDB_LICENSE_FILES = LICENSE
define MICROSTICK_REGDB_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0644 $(@D)/regulatory.db $(TARGET_DIR)/lib/firmware/regulatory.db
	$(INSTALL) -D -m 0644 $(@D)/regulatory.db.p7s $(TARGET_DIR)/lib/firmware/regulatory.db.p7s
endef
$(eval $(generic-package))
