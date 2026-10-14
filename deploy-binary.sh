#!/bin/sh

set -eu

REPO="yibaiba/hideck"
SOURCE_BASE_URL="https://raw.githubusercontent.com/yibaiba/hideck/main"
RELEASES_API_URL="https://api.github.com/repos/${REPO}/releases/latest"
RELEASES_LATEST_URL="https://github.com/${REPO}/releases/latest"
RELEASES_DOWNLOAD_URL="https://github.com/${REPO}/releases/download"
USER_AGENT="hideck-deploy-binary/1.0 (+https://github.com/${REPO})"

require_command() {
  command -v "$1" >/dev/null 2>&1 || {
    printf '缺少 %s，无法部署 HiDeck。\n' "$1" >&2
    exit 1
  }
}

resolve_project_dir() {
  if [ -n "${HIDECK_DIR:-}" ]; then
    printf '%s\n' "$HIDECK_DIR"
    return
  fi

  script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
  if [ -f "$script_dir/config/config.example.yaml" ] && [ -f "$script_dir/deploy-binary.sh" ]; then
    printf '%s\n' "$script_dir"
    return
  fi

  printf '%s/hideck\n' "$PWD"
}

detect_os() {
  os=$(uname -s)
  case "$os" in
    Linux) ;;
    *)
      printf '二进制部署脚本只支持 Linux，当前系统：%s\n' "$os" >&2
      exit 1
      ;;
  esac
}

is_openwrt() {
  [ -f /etc/openwrt_release ] && return 0
  [ -f /etc/os-release ] && grep -qi '^ID=openwrt' /etc/os-release && return 0
  return 1
}

cpu_arch() {
  machine=$(uname -m)
  case "$machine" in
    x86_64|amd64) printf 'amd64\n' ;;
    aarch64|arm64) printf 'arm64\n' ;;
    armv7l|armv7|armhf) printf 'armv7\n' ;;
    *)
      printf '无法识别的 CPU 架构：%s\n请设置 HIDECK_ARCH=linux_amd64|linux_arm64|linux_armv7 或 openwrt_amd64|openwrt_arm64|openwrt_armv7\n' "$machine" >&2
      exit 1
      ;;
  esac
}

detect_arch() {
  if [ -n "${HIDECK_ARCH:-}" ]; then
    case "$HIDECK_ARCH" in
      openwrt_amd64|openwrt_arm64|openwrt_armv7) printf '%s\n' "$HIDECK_ARCH" ;;
      linux_amd64) printf 'linux_amd64\n' ;;
      linux_arm64) printf 'linux_arm64\n' ;;
      linux_armv7) printf 'linux_armv7\n' ;;
      amd64|x86_64)
        if is_openwrt; then printf 'openwrt_amd64\n'; else printf 'linux_amd64\n'; fi ;;
      arm64|aarch64)
        if is_openwrt; then printf 'openwrt_arm64\n'; else printf 'linux_arm64\n'; fi ;;
      armv7|armv7l|armhf)
        if is_openwrt; then printf 'openwrt_armv7\n'; else printf 'linux_armv7\n'; fi ;;
      *)
        printf '不支持的 HIDECK_ARCH：%s\n' "$HIDECK_ARCH" >&2
        exit 1
        ;;
    esac
    return
  fi

  cpu=$(cpu_arch)
  if is_openwrt; then
    printf 'openwrt_%s\n' "$cpu"
  else
    printf 'linux_%s\n' "$cpu"
  fi
}

curl_github() {
  curl -fL --retry 3 --retry-delay 1 -A "$USER_AGENT" "$@"
}

normalize_version() {
  version=$1
  case "$version" in
    ""|latest) printf 'latest\n' ;;
    v*) printf '%s\n' "$version" ;;
    *) printf 'v%s\n' "$version" ;;
  esac
}

extract_tag_name() {
  printf '%s\n' "$1" | sed -n 's#.*/releases/tag/\([^/?#]*\).*#\1#p' | head -n 1
}

resolve_latest_version() {
  latest=$( { curl_github -sS "$RELEASES_API_URL" || true; } | sed -n 's/.*"tag_name":[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)
  if [ -n "$latest" ]; then
    printf '%s\n' "$latest"
    return
  fi

  latest=$(extract_tag_name "$(curl_github -sS -o /dev/null -w '%{url_effective}' "$RELEASES_LATEST_URL" || true)")
  if [ -n "$latest" ]; then
    printf '%s\n' "$latest"
    return
  fi

  printf '无法从 GitHub Releases 读取最新版本。\n可设置 HIDECK_VERSION=v2.1.1 后重试。\n' >&2
  exit 1
}

resolve_version() {
  requested=$(normalize_version "${HIDECK_VERSION:-latest}")
  if [ "$requested" != "latest" ]; then
    printf '%s\n' "$requested"
    return
  fi
  resolve_latest_version
}

download_file() {
  source_url=$1
  target_file=$2
  file_mode=$3

  require_command curl
  temporary_file=$(mktemp "${target_file}.tmp.XXXXXX")
  if ! curl_github "$source_url" -o "$temporary_file"; then
    rm -f "$temporary_file"
    printf '下载失败：%s\n' "$source_url" >&2
    exit 1
  fi
  chmod "$file_mode" "$temporary_file"
  mv "$temporary_file" "$target_file"
}

try_download_file() {
  source_url=$1
  target_file=$2
  file_mode=$3

  temporary_file=$(mktemp "${target_file}.tmp.XXXXXX")
  if curl_github "$source_url" -o "$temporary_file"; then
    chmod "$file_mode" "$temporary_file"
    mv "$temporary_file" "$target_file"
    return 0
  fi
  rm -f "$temporary_file"
  return 1
}

download_if_missing() {
  target_file=$1
  source_url=$2
  file_mode=$3

  if [ -f "$target_file" ]; then
    printf '保留现有文件：%s\n' "$target_file"
    return
  fi
  download_file "$source_url" "$target_file" "$file_mode"
  printf '已下载：%s\n' "$target_file"
}

file_sha256() {
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$1" | awk '{print $1}'
    return
  fi
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$1" | awk '{print $1}'
    return
  fi
  printf '缺少 sha256sum 或 shasum，无法校验下载文件。\n' >&2
  exit 1
}

checksum_for_asset() {
  sums_file=$1
  asset_name=$2
  awk -v name="$asset_name" '
    NF >= 2 {
      n = $NF
      sub(/^.*\//, "", n)
      if (n == name) {
        print $1
        exit
      }
    }
  ' "$sums_file"
}

verify_checksum() {
  sums_file=$1
  sidecar_file=$2
  asset_name=$3
  binary_file=$4

  actual=$(file_sha256 "$binary_file")
  expected=""
  source_name=""

  if [ -f "$sidecar_file" ]; then
    expected=$(checksum_for_asset "$sidecar_file" "$asset_name")
    source_name="${asset_name}.sha256"
  fi
  if [ -z "$expected" ] && [ -f "$sums_file" ]; then
    expected=$(checksum_for_asset "$sums_file" "$asset_name")
    source_name="SHA256SUMS"
  fi
  if [ -z "$expected" ]; then
    printf '找不到 %s 的校验文件（SHA256SUMS 或 %s.sha256）。\n' "$asset_name" "$asset_name" >&2
    exit 1
  fi
  if [ "$expected" != "$actual" ]; then
    printf '校验失败：%s\n来源 %s\n期望 %s\n实际 %s\n文件可能还在上传，或校验清单和二进制不是同一批。请稍后重试，或设置 HIDECK_VERSION 指定版本。\n' \
      "$asset_name" "$source_name" "$expected" "$actual" >&2
    exit 1
  fi
  printf '校验通过：%s（%s）\n' "$asset_name" "$source_name"
}

write_procd_init() {
  init_file=$1
  binary_path=$2
  config_file=$3
  working_dir=$4

  temporary_file=$(mktemp "${init_file}.tmp.XXXXXX")
  cat >"$temporary_file" <<EOF
#!/bin/sh /etc/rc.common

START=99
STOP=10
USE_PROCD=1

PROG=${binary_path}
CONFIG=${config_file}
WORKDIR=${working_dir}

start_service() {
	mkdir -p "\$WORKDIR/data" "\$WORKDIR/logs"

	procd_open_instance
	procd_set_param command "\$PROG" -c "\$CONFIG"
	procd_set_param respawn 3600 5 5
	procd_set_param stdout 1
	procd_set_param stderr 1
	procd_set_param file "\$CONFIG"
	procd_set_param limits core="0"
	procd_set_param env HOME="\$WORKDIR"
	procd_set_param cwd "\$WORKDIR"
	procd_close_instance
}

service_triggers() {
	procd_add_reload_trigger hideck
}
EOF
  chmod 755 "$temporary_file"
  mv "$temporary_file" "$init_file"
}

install_openwrt_service() {
  init_source=$1
  if maybe_sudo install -m 755 "$init_source" /etc/init.d/hideck; then
    maybe_sudo /etc/init.d/hideck enable || true
    maybe_sudo /etc/init.d/hideck start || true
    printf '已安装并启动 procd 服务：/etc/init.d/hideck\n'
    return
  fi
  printf '没有写入 /etc/init.d 的权限。初始化脚本已生成：%s\n可用下面命令安装：\n  sudo cp %s /etc/init.d/hideck\n  sudo /etc/init.d/hideck enable\n  sudo /etc/init.d/hideck start\n或前台运行：\n  %s -c %s\n' \
    "$init_source" "$init_source" "$BINARY_PATH" "$CONFIG_FILE"
}

install_openwrt_packages() {
  if command -v opkg >/dev/null 2>&1; then
    maybe_sudo opkg update || return 1
    openwrt_installer="maybe_sudo opkg install"
  elif command -v apk >/dev/null 2>&1; then
    maybe_sudo apk update || return 1
    openwrt_installer="maybe_sudo apk add"
  else
    printf 'OpenWrt 缺少 opkg/apk，无法安装运行依赖。\n' >&2
    return 1
  fi
  openwrt_dependencies_failed=0
  install_packages "$openwrt_installer" \
    libqmi kmod-usb-net-qmi-wwan kmod-usb-serial-option \
    lame-lib libopencore-amrnb libopencore-amrwb libvo-amrwbenc || openwrt_dependencies_failed=1
  if modem_voice_requested; then
    printf 'OpenWrt 模组直拨需要配套 hideck-adb 安装包；官方旧版 adb 不会替代它。\n'
    install_packages "$openwrt_installer" hideck-adb alsa-utils kmod-usb-audio || openwrt_dependencies_failed=1
  fi
  return "$openwrt_dependencies_failed"
}

write_systemd_unit() {
  unit_file=$1
  working_dir=$2
  binary_path=$3
  config_file=$4

  temporary_file=$(mktemp "${unit_file}.tmp.XXXXXX")
  cat >"$temporary_file" <<EOF
[Unit]
Description=HiDeck modem management service
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=${working_dir}
ExecStart=${binary_path} -c ${config_file}
Restart=on-failure
RestartSec=5s
KillMode=control-group
TimeoutStopSec=30s

[Install]
WantedBy=multi-user.target
EOF
  chmod 644 "$temporary_file"
  mv "$temporary_file" "$unit_file"
}

maybe_sudo() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
    return
  fi
  if command -v sudo >/dev/null 2>&1 && sudo -n true >/dev/null 2>&1; then
    sudo "$@"
    return
  fi
  return 1
}

install_systemd_service() {
  unit_source=$1
  if ! command -v systemctl >/dev/null 2>&1; then
    printf '未检测到 systemd。可手动运行：\n  %s -c %s\n' "$BINARY_PATH" "$CONFIG_FILE"
    return
  fi

  if maybe_sudo install -m 644 "$unit_source" /etc/systemd/system/hideck.service; then
    maybe_sudo systemctl daemon-reload
    maybe_sudo systemctl enable --now hideck.service
    maybe_sudo systemctl --no-pager --full status hideck.service || true
    printf '已安装并启动 systemd 服务：hideck.service\n'
    return
  fi

  printf '没有 systemd 写入权限。单元文件已生成：%s\n可用下面命令安装：\n  sudo cp %s /etc/systemd/system/hideck.service\n  sudo systemctl daemon-reload\n  sudo systemctl enable --now hideck.service\n或前台运行：\n  %s -c %s\n' \
    "$unit_source" "$unit_source" "$BINARY_PATH" "$CONFIG_FILE"
}

install_runtime_dependencies() {
  printf '正在安装所选运行依赖…\n'
  dependencies_failed=0
  if ! install_runtime_dependencies_with_pkg; then
    printf '运行依赖未装全，请处理上面的包管理器错误。\n' >&2
    dependencies_failed=1
  fi
  if modem_voice_requested; then
    if ! check_modem_voice_tools; then dependencies_failed=1; fi
    if ! check_usb_audio_support; then dependencies_failed=1; fi
  else
    printf '未选择模组直拨依赖；OpenWrt 可另装 hideck-modem-voice 配套包。\n'
  fi
  if [ "$dependencies_failed" -eq 0 ]; then
    printf '所选运行依赖检查通过；未执行真实通话测试。\n'
  fi
  return "$dependencies_failed"
}

modem_voice_requested() {
  case "${HIDECK_MODEM_VOICE:-auto}" in
    1) return 0 ;;
    0) return 1 ;;
    auto) ! is_openwrt ;;
  esac
}

resolve_modem_voice_adb() {
  voice_adb=/usr/libexec/hideck/adb
  if [ -e "$voice_adb" ] || [ -L "$voice_adb" ]; then
    if [ ! -x "$voice_adb" ]; then
      printf 'HiDeck 专用 ADB 不可执行：%s\n' "$voice_adb" >&2
      return 1
    fi
    return 0
  fi
  voice_adb=adb
  command -v "$voice_adb" >/dev/null 2>&1 || {
    printf '缺少 adb；OpenWrt 请先安装配套 hideck-adb 包。\n' >&2
    return 1
  }
}

check_modem_voice_tools() {
  resolve_modem_voice_adb || return 1
  for voice_tool in arecord aplay; do
    if ! command -v "$voice_tool" >/dev/null 2>&1; then
      printf '缺少 %s，模组直拨不可用。请安装 ADB 和 alsa-utils。\n' "$voice_tool" >&2
      return 1
    fi
  done
  # Help/version commands never start an ADB server or open a PCM device.
  if ! adb_help=$("$voice_adb" help 2>&1); then
    printf '无法执行 adb help：%s\n' "$adb_help" >&2
    return 1
  fi
  if ! printf '%s\n' "$adb_help" | grep -Eq '^[[:space:]]*-t[[:space:]]'; then
    printf 'ADB 不支持 transport-id（-t）；请升级 ADB，旧版 OpenWrt adb 不能用于模组直拨。\n' >&2
    return 1
  fi
  if ! printf '%s\n' "$adb_help" | grep -Eq '^[[:space:]]*-L[[:space:]]'; then
    printf 'ADB 不支持独立 server socket（-L）；请升级 ADB 后再使用模组直拨。\n' >&2
    return 1
  fi
  "$voice_adb" version && arecord --version && aplay --version
}

check_usb_audio_support() {
  # A disconnected modem has no /dev/snd nodes. Check the driver, not devices.
  if [ -d /sys/bus/usb/drivers/snd-usb-audio ]; then
    return 0
  fi
  if command -v modinfo >/dev/null 2>&1 && modinfo snd_usb_audio >/dev/null 2>&1; then
    return 0
  fi
  printf '无法确认宿主机支持 snd_usb_audio。请安装与运行内核匹配的 USB 音频驱动（OpenWrt：kmod-usb-audio）；无需重启模组。\n' >&2
  return 1
}

install_packages() {
  installer=$1
  shift
  failed=0
  for package in "$@"; do
    if ! $installer "$package"; then
      printf '未安装：%s\n' "$package"
      failed=1
    fi
  done
  return "$failed"
}

install_runtime_dependencies_with_pkg() {
  # OpenWrt also uses apk; its package names are not Alpine package names.
  if is_openwrt; then
    install_openwrt_packages
    return $?
  fi
  for package_manager in apt-get dnf yum apk pacman; do
    command -v "$package_manager" >/dev/null 2>&1 || continue
    adb_package=android-tools
    recording_packages='lame-libs opencore-amr vo-amrwbenc'
    case "$package_manager" in
      apt-get)
        maybe_sudo apt-get update -y || return 1
        package_operation='install -y'
        adb_package=adb
        recording_packages='libmp3lame0 libopencore-amrnb0 libopencore-amrwb0 libvo-amrwbenc0'
        ;;
      dnf|yum) package_operation='install -y' ;;
      apk)
        package_operation='add --no-cache'
        adb_package=android-tools-adb
        ;;
      pacman)
        package_operation='-S --needed --noconfirm'
        recording_packages='lame opencore-amr vo-amrwbenc'
        ;;
    esac
    voice_packages=
    if modem_voice_requested; then voice_packages="$adb_package alsa-utils kmod"; fi
    # These word lists contain only the fixed package names defined above.
    install_packages "maybe_sudo $package_manager $package_operation" $voice_packages $recording_packages
    return $?
  done
  printf '未识别的包管理器，请自行安装 ADB、alsa-utils、USB 音频驱动及 AMR/MP3 库。\n' >&2
  return 1
}

detect_os
case "${HIDECK_MODEM_VOICE:-auto}" in
  auto|0|1) ;;
  *) printf 'HIDECK_MODEM_VOICE 必须为 auto、0 或 1。\n' >&2; exit 1 ;;
esac
require_command curl
require_command uname

ARCH=$(detect_arch)
VERSION=$(resolve_version)
ASSET_NAME="hideck_${VERSION}_${ARCH}"

if is_openwrt && [ -z "${HIDECK_DIR:-}" ]; then
  PROJECT_DIR=/var/lib/hideck
  CONFIG_DIR=/etc/hideck
  CONFIG_FILE="$CONFIG_DIR/config.yaml"
  CONFIG_EXAMPLE="$CONFIG_DIR/config.example.yaml"
  BINARY_PATH=/usr/bin/hideck
else
  PROJECT_DIR=$(resolve_project_dir)
fi
mkdir -p "$PROJECT_DIR"
PROJECT_DIR=$(CDPATH= cd -- "$PROJECT_DIR" && pwd)
if [ -z "${CONFIG_DIR:-}" ]; then
  CONFIG_DIR="$PROJECT_DIR/config"
  CONFIG_FILE="$CONFIG_DIR/config.yaml"
  CONFIG_EXAMPLE="$CONFIG_DIR/config.example.yaml"
  BINARY_PATH="$PROJECT_DIR/hideck"
fi
UNIT_FILE="$PROJECT_DIR/hideck.service"
INIT_FILE="$PROJECT_DIR/hideck.init"
ASSET_URL="${RELEASES_DOWNLOAD_URL}/${VERSION}/${ASSET_NAME}"
SUMS_URL="${RELEASES_DOWNLOAD_URL}/${VERSION}/SHA256SUMS"
SIDECAR_URL="${RELEASES_DOWNLOAD_URL}/${VERSION}/${ASSET_NAME}.sha256"

printf '部署目录：%s\n版本：%s\n架构：%s\n' "$PROJECT_DIR" "$VERSION" "$ARCH"

mkdir -p "$PROJECT_DIR/data" "$PROJECT_DIR/logs" 2>/dev/null || maybe_sudo mkdir -p "$PROJECT_DIR/data" "$PROJECT_DIR/logs"
mkdir -p "$CONFIG_DIR" 2>/dev/null || maybe_sudo mkdir -p "$CONFIG_DIR"
if is_openwrt; then
  EXAMPLE_URL="$SOURCE_BASE_URL/packaging/openwrt/hideck/files/config.yaml"
else
  EXAMPLE_URL="$SOURCE_BASE_URL/config/config.example.yaml"
fi
download_if_missing "$CONFIG_EXAMPLE" "$EXAMPLE_URL" 644
if [ -f "$CONFIG_FILE" ]; then
  printf '保留现有配置：%s\n' "$CONFIG_FILE"
else
  if [ ! -f "$CONFIG_EXAMPLE" ]; then
    printf '缺少配置模板：%s\n' "$CONFIG_EXAMPLE" >&2
    exit 1
  fi
  temporary_file=$(mktemp "$CONFIG_DIR/config.yaml.tmp.XXXXXX")
  cp "$CONFIG_EXAMPLE" "$temporary_file"
  chmod 600 "$temporary_file"
  if ! mv "$temporary_file" "$CONFIG_FILE" 2>/dev/null; then
    maybe_sudo mv "$temporary_file" "$CONFIG_FILE"
  fi
  printf '已创建配置：%s\n' "$CONFIG_FILE"
fi

DOWNLOAD_DIR=$(mktemp -d "${TMPDIR:-/tmp}/hideck-binary.XXXXXX")
trap 'rm -rf "$DOWNLOAD_DIR"' EXIT
download_file "$ASSET_URL" "$DOWNLOAD_DIR/$ASSET_NAME" 755
if ! try_download_file "$SIDECAR_URL" "$DOWNLOAD_DIR/${ASSET_NAME}.sha256" 644; then
  printf '未找到 %s.sha256，改用 SHA256SUMS。\n' "$ASSET_NAME"
fi
if ! try_download_file "$SUMS_URL" "$DOWNLOAD_DIR/SHA256SUMS" 644; then
  printf '未找到 SHA256SUMS。\n'
fi
verify_checksum "$DOWNLOAD_DIR/SHA256SUMS" "$DOWNLOAD_DIR/${ASSET_NAME}.sha256" "$ASSET_NAME" "$DOWNLOAD_DIR/$ASSET_NAME"

if ! install -m 755 "$DOWNLOAD_DIR/$ASSET_NAME" "$BINARY_PATH" 2>/dev/null; then
  maybe_sudo install -m 755 "$DOWNLOAD_DIR/$ASSET_NAME" "$BINARY_PATH"
fi
printf '已安装二进制：%s\n' "$BINARY_PATH"
dependency_status=0
if ! install_runtime_dependencies; then
  dependency_status=1
  printf '依赖检查未通过，继续安装 HiDeck 服务；这不表示模组直拨或录音已可用。\n' >&2
fi

if is_openwrt; then
  write_procd_init "$INIT_FILE" "$BINARY_PATH" "$CONFIG_FILE" "$PROJECT_DIR"
  install_openwrt_service "$INIT_FILE"
else
  write_systemd_unit "$UNIT_FILE" "$PROJECT_DIR" "$BINARY_PATH" "$CONFIG_FILE"
  install_systemd_service "$UNIT_FILE"
fi

printf '\n浏览器打开：http://YOUR_IP:7575\n默认账号：admin / admin，首次登录后请立即改密。\n'
if [ "$dependency_status" -ne 0 ]; then
  printf '部署存在未解决的运行依赖问题，请按上述错误补齐后重新检查。\n' >&2
fi
exit "$dependency_status"
