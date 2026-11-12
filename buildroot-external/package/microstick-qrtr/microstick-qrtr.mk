MICROSTICK_QRTR_VERSION = 9dc7a88548c27983e06465d3fbba2ba27d4bc050
MICROSTICK_QRTR_SOURCE = qrtr-$(MICROSTICK_QRTR_VERSION).tar.gz
MICROSTICK_QRTR_SITE = $(call github,linux-msm,qrtr,$(MICROSTICK_QRTR_VERSION))
MICROSTICK_QRTR_LICENSE = BSD-3-Clause
MICROSTICK_QRTR_LICENSE_FILES = LICENSE
MICROSTICK_QRTR_INSTALL_STAGING = YES

define MICROSTICK_QRTR_BUILD_CMDS
	$(TARGET_MAKE_ENV) $(MAKE) $(TARGET_CONFIGURE_OPTS) CFLAGS="$(TARGET_CFLAGS)" LDFLAGS="$(TARGET_LDFLAGS)" -C $(@D) all
endef

define MICROSTICK_QRTR_INSTALL_STAGING_CMDS
	$(INSTALL) -D -m 0644 $(@D)/lib/libqrtr.h $(STAGING_DIR)/usr/include/libqrtr.h
	$(INSTALL) -D -m 0755 $(@D)/libqrtr.so $(STAGING_DIR)/usr/lib/libqrtr.so.1.0
	ln -sf libqrtr.so.1.0 $(STAGING_DIR)/usr/lib/libqrtr.so.1
	ln -sf libqrtr.so.1 $(STAGING_DIR)/usr/lib/libqrtr.so
endef

define MICROSTICK_QRTR_INSTALL_TARGET_CMDS
	$(INSTALL) -D -m 0755 $(@D)/libqrtr.so $(TARGET_DIR)/usr/lib/libqrtr.so.1.0
	ln -sf libqrtr.so.1.0 $(TARGET_DIR)/usr/lib/libqrtr.so.1
	$(foreach tool,qrtr-ns qrtr-lookup qrtr-cfg,$(INSTALL) -D -m 0755 $(@D)/$(tool) $(TARGET_DIR)/usr/bin/$(tool)$(sep))
endef
$(eval $(generic-package))
