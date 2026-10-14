include $(TOPDIR)/rules.mk

PKG_NAME:=hideck
HIDECK_VERSION?=2.1.23
HIDECK_PACKAGE_VERSION?=$(HIDECK_VERSION)
PKG_VERSION:=$(HIDECK_PACKAGE_VERSION)
PKG_RELEASE:=2

PKG_LICENSE:=SEE LICENSE
PKG_MAINTAINER:=yibaiba <https://github.com/yibaiba/hideck>

ifeq ($(ARCH),x86_64)
  HIDECK_BINARCH:=amd64
else ifeq ($(ARCH),aarch64)
  HIDECK_BINARCH:=arm64
else ifeq ($(ARCH),arm)
  HIDECK_BINARCH:=armv7
else
ifneq ($(DUMP),1)
  $(error HiDeck OpenWrt package supports x86_64, aarch64, and arm)
endif
endif

PKG_SOURCE:=hideck_v$(HIDECK_VERSION)_openwrt_$(HIDECK_BINARCH)
PKG_SOURCE_URL:=https://github.com/yibaiba/hideck/releases/download/v$(HIDECK_VERSION)
# Supplied by build-packages.sh from the verified local release artifact.
PKG_HASH:=$(HIDECK_BINARY_SHA256)

include $(INCLUDE_DIR)/package.mk

define Package/hideck
  SECTION:=net
  CATEGORY:=Network
  TITLE:=HiDeck modem management
  URL:=https://github.com/yibaiba/hideck
  DEPENDS:=+libqmi +kmod-usb-net-qmi-wwan +kmod-usb-serial-option
endef

define Package/hideck/description
  Prebuilt musl-static HiDeck for OpenWrt. Do not use the UPX GitHub
  linux_* binaries; those need glibc.
endef

define Package/hideck/conffiles
/etc/hideck/config.yaml
endef

define Package/hideck-modem-voice
  SECTION:=net
  CATEGORY:=Network
  TITLE:=HiDeck optional module direct calling dependencies
  DEPENDS:=+hideck +hideck-adb +alsa-utils +kmod-usb-audio
endef

define Package/hideck-modem-voice/description
  Optional USB audio and modern private ADB for supported module firmware.
  WiFi calling and other HiDeck features do not require this package.
endef

define Build/Prepare
	test -n "$(PKG_HASH)"
	printf '%s  %s\n' "$(PKG_HASH)" "$(DL_DIR)/$(PKG_SOURCE)" | sha256sum -c -
	mkdir -p $(PKG_BUILD_DIR)
	cp $(DL_DIR)/$(PKG_SOURCE) $(PKG_BUILD_DIR)/hideck
	chmod 0755 $(PKG_BUILD_DIR)/hideck
endef

define Build/Compile
endef

define Package/hideck/install
	$(INSTALL_DIR) $(1)/usr/bin $(1)/etc/hideck $(1)/etc/init.d $(1)/var/lib/hideck
	$(INSTALL_BIN) $(PKG_BUILD_DIR)/hideck $(1)/usr/bin/hideck
	$(INSTALL_CONF) ./files/config.yaml $(1)/etc/hideck/config.yaml
	$(INSTALL_BIN) ./files/hideck.init $(1)/etc/init.d/hideck
endef

define Package/hideck-modem-voice/install
	$(INSTALL_DIR) $(1)/usr/share/hideck
	$(INSTALL_DATA) ./files/modem-voice.txt $(1)/usr/share/hideck/modem-voice.txt
endef

$(eval $(call BuildPackage,hideck))
$(eval $(call BuildPackage,hideck-modem-voice))
