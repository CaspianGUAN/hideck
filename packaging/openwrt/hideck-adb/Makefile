include $(TOPDIR)/rules.mk

PKG_NAME:=hideck-adb
PKG_VERSION:=37.0.0
PKG_RELEASE:=1
PKG_SOURCE:=android-tools-$(PKG_VERSION).tar.xz
PKG_SOURCE_URL:=https://github.com/nmeum/android-tools/releases/download/$(PKG_VERSION)
PKG_HASH:=2725d09f892a3a38e534429f47a321f58ecf6a3169caa42c915fb2cb7d46be0e
PKG_BUILD_DIR:=$(BUILD_DIR)/android-tools-$(PKG_VERSION)
PKG_LICENSE:=Apache-2.0 MIT BSD-3-Clause ISC OpenSSL
PKG_LICENSE_FILES:=LICENSE vendor/boringssl/LICENSE vendor/fmtlib/LICENSE \
	vendor/boringssl/third_party/googletest/LICENSE hideck-brotli/LICENSE
PKG_BUILD_DEPENDS:=protobuf/host pcre2
PKG_BUILD_PARALLEL:=1
HIDECK_ADB_JOBS?=2

BROTLI_SOURCE:=hideck-brotli-1.2.0.tar.gz

include $(INCLUDE_DIR)/package.mk
include $(INCLUDE_DIR)/cmake.mk

CMAKE_BINARY_SUBDIR:=build
# Install just our private adb in Package/install, not every upstream tool.
PKG_INSTALL:=

define Download/brotli
  FILE:=$(BROTLI_SOURCE)
  URL:=https://codeload.github.com/google/brotli/tar.gz/refs/tags
  URL_FILE:=v1.2.0
  HASH:=816c96e8e8f193b40151dad7e8ff37b1221d019dbcb9c35cd3fadbfe6477dfec
endef
$(eval $(call Download,brotli))

define Package/hideck-adb
  SECTION:=utils
  CATEGORY:=Utilities
  TITLE:=Private modern ADB for HiDeck module voice
  URL:=https://github.com/nmeum/android-tools
  DEPENDS:=+libstdcpp +libatomic +libusb-1.0 +liblz4 +libzstd +protobuf +zlib
endef

define Package/hideck-adb/description
  Modern ADB installed under /usr/libexec/hideck, without replacing system adb.
  Only the ADB target is built; no daemon is started during installation.
endef

CMAKE_OPTIONS += \
	-DANDROID_TOOLS_PATCH_VENDOR=OFF \
	-DANDROID_TOOLS_USE_BUNDLED_FMT=ON \
	-DBUILD_SHARED_LIBS=OFF \
	-DProtobuf_PROTOC_EXECUTABLE=$(STAGING_DIR_HOSTPKG)/bin/protoc

define Build/Prepare
	$(call Build/Prepare/Default)
	mkdir -p $(PKG_BUILD_DIR)/hideck-brotli
	tar -xzf $(DL_DIR)/$(BROTLI_SOURCE) -C $(PKG_BUILD_DIR)/hideck-brotli --strip-components=1
endef

define Build/Compile
	$(call Build/Compile/Default,-j$(HIDECK_ADB_JOBS) adb)
endef

define Package/hideck-adb/install
	$(INSTALL_DIR) $(1)/usr/libexec/hideck $(1)/usr/share/licenses/hideck-adb
	$(INSTALL_BIN) $(CMAKE_BINARY_DIR)/vendor/adb $(1)/usr/libexec/hideck/adb
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/LICENSE $(1)/usr/share/licenses/hideck-adb/android-tools
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/boringssl/LICENSE $(1)/usr/share/licenses/hideck-adb/boringssl
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/boringssl/third_party/googletest/LICENSE $(1)/usr/share/licenses/hideck-adb/googletest
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/vendor/fmtlib/LICENSE $(1)/usr/share/licenses/hideck-adb/fmt
	$(INSTALL_DATA) $(PKG_BUILD_DIR)/hideck-brotli/LICENSE $(1)/usr/share/licenses/hideck-adb/brotli
endef

$(eval $(call BuildPackage,hideck-adb))
