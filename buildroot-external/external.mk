include $(sort $(wildcard $(BR2_EXTERNAL_MICROSTICK_PATH)/package/*/*.mk))

# Enable the Qualcomm SoC and generic ModemManager plugins.
MODEM_MANAGER_CONF_OPTS += -Dauto_features=disabled -Dplugin_qcom_soc=enabled -Dplugin_generic=enabled
# Build only the two util-linux tools needed on this board.
UTIL_LINUX_CONF_OPTS += --disable-all-programs --enable-blkid --enable-rfkill
