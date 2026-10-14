# OpenWrt 安装与打包

OpenWrt 必须使用 musl 构建、无 UPX 的程序，不能使用依赖 glibc 的 `linux_*`。Release 同时发布静态 `hideck_*_openwrt_*` 和动态 `hideck_*_openwrt_dynamic_*` 二进制；IPK/APK 安装包和二进制安装脚本继续使用静态版本。需要通话 MP3 编码时，按下文要求使用动态版本。程序及语音资源体积较大，小闪存设备需要 extroot；不提供 MIPS 包。

## 三个独立安装包

| 包 | 内容 | 是否必须 |
| --- | --- | --- |
| `hideck` | 程序、配置、procd 服务，依赖 QMI 与 USB 串口组件 | 是 |
| `hideck-adb` | 新版 ADB，安装到 `/usr/libexec/hideck/adb` | 模组直拨需要 |
| `hideck-modem-voice` | 可选依赖包，引入 `hideck-adb`、`alsa-utils`、`kmod-usb-audio` | 模组直拨需要 |

仅使用 WiFi Calling、短信等功能时不必安装后两个包。安装包不会切换通话模式、修改 USB 配置或重启模组。

`hideck-adb` 从 [android-tools](https://github.com/nmeum/android-tools) 固定版本源码编译，不覆盖 `/usr/bin/adb`、不在安装时启动 ADB 服务。HiDeck 优先使用专用路径，没有安装时才查找系统 ADB；专用程序存在但不可执行时会报错。

[OpenWrt 官方 adb 配方](https://github.com/openwrt/openwrt/blob/main/package/utils/adb/Makefile)基于旧版 Android，缺少需要的 `-t`、`-L` 参数，不能代替此包。模组语音资源已内嵌，但宿主机仍需要新版 ADB、ALSA 和内核 USB 音频支持。

24.10 的旧版 libusb 不提供 USB 20 Gbit/s 速率详情接口，本配方按库版本编译该项可选速率展示；USB 枚举和传输保持原实现。使用 libusb 1.0.29 及以上时保留完整速率详情。

## 选择与安装

发布工作流生成 `hideck_<版本>_openwrt-packages_<SDK版本>-<目标>.tar.gz`。是否已经发布以对应 GitHub Release 附件为准。

当前矩阵见 [sdk-matrix.json](sdk-matrix.json)：OpenWrt 24.10.8（IPK）与 25.12.5（APK），目标为 `x86/64`、`armsr/armv8`、`mvebu/cortexa9`。**相同 CPU 位数不等于相同包架构**，例如不能把 `armsr/armv8` 包强行装进任意 aarch64 固件。其他目标应使用对应官方 SDK 自行构建。

先检查设备 `/etc/openwrt_release`、`opkg print-architecture` 或 `apk --print-arch`。下载匹配的包集合、核对 Release 的 SHA256SUMS，解压后在目录内再次执行 `sha256sum -c SHA256SUMS`。不要在不同 OpenWrt 版本间混装，也不要使用 `--force-depends` 跳过内核 ABI 检查；内核组件由当前固件自己的软件源提供。

### OpenWrt 24.10：IPK

```sh
opkg update
# 只装主程序：
opkg install ./hideck_*.ipk
# 需要模组直拨时，再装两个可选包：
opkg install ./hideck-adb_*.ipk ./hideck-modem-voice_*.ipk
/etc/init.d/hideck enable
/etc/init.d/hideck start
```

### OpenWrt 25.12：APK

APK 集合使用每次构建独立生成的公钥签名，`keys/hideck-build.pem` 是公钥，私钥不会进入附件。确认附件来自可信 Release 后，为本次安装建立包含系统公钥和该构建公钥的目录，不全局关闭签名验证：

```sh
mkdir -p trusted-keys
cp /etc/apk/keys/* trusted-keys/
cp keys/hideck-build.pem trusted-keys/
apk update
apk --keys-dir "$PWD/trusted-keys" verify ./hideck-*.apk
# 主程序文件名以 hideck-数字 开头，不会匹配两个可选包：
apk --keys-dir "$PWD/trusted-keys" add ./hideck-[0-9]*.apk
# 可选：
apk --keys-dir "$PWD/trusted-keys" add ./hideck-adb-*.apk ./hideck-modem-voice-*.apk
/etc/init.d/hideck enable
/etc/init.d/hideck start
```

公钥随构建变化；升级时应验证新附件并使用其公钥，不需要永久信任每次构建的密钥。这些不是 OpenWrt 官方签名包。

### 安装后检查（不操作模组）

```sh
/usr/libexec/hideck/adb version
/usr/libexec/hideck/adb help
arecord --version
aplay --version
opkg list-installed kmod-usb-audio   # APK 系统使用 apk info kmod-usb-audio
```

安装成功不代表任意模组都支持直拨。目前音频适配仍以项目支持的模组固件为准；也不代表已完成真实通话测试。

## 继续使用二进制部署脚本

```sh
opkg update && opkg install curl  # APK 系统：apk update && apk add curl
curl -fsSL https://raw.githubusercontent.com/yibaiba/hideck/main/deploy-binary.sh | sh
```

脚本自动选用 musl 静态二进制，默认安装 QMI、USB 串口和录音依赖，**OpenWrt 默认不安装模组直拨依赖**。若使用二进制脚本且需要直拨，先从匹配的包集合单独安装 `hideck-adb`，再运行：

```sh
curl -fsSL https://raw.githubusercontent.com/yibaiba/hideck/main/deploy-binary.sh | HIDECK_MODEM_VOICE=1 sh
```

这会检查/安装 `hideck-adb`、ALSA、USB 音频依赖并验证工具能力；官方源没有 `hideck-adb` 时明确失败，不会拿旧版 `adb` 替代。不要把包管理安装和脚本安装混用于同一个主程序；选择一种升级方式。

录音编解码库与 USB PCM 是两类依赖：现有脚本还会请求 `lame-lib`、`libopencore-amrnb`、`libopencore-amrwb`、`libvo-amrwbenc`。本次固定的标准 feeds 未提供上述 AMR 包，未额外制作编解码器包；使用缺少它们的软件源时脚本会明确报告录音依赖未装全。三个安装包不代表 AMR 录音已验收，也不会把此限制误报为模组 PCM 已通过通话测试。

配置：`/etc/hideck/config.yaml`；数据：`/var/lib/hideck`；服务：`/etc/init.d/hideck`。OpenWrt 的 `/var` 通常在内存中，需要持久数据时请在配置中指定持久挂载目录。`qmi-proxy` 默认 `/usr/libexec/qmi-proxy`，保持 `system.openwrt_dynamic_interfaces: true`。

<a id="openwrt-dynamic-musl"></a>

## 原生动态 musl 二进制（通话录音）

HiDeck 使用动态加载的编码库生成 MP3。静态 musl 程序调用 `dlopen` 会报
`Dynamic loading not supported`；仅安装 `lame-lib` 不能解决这一问题。
Release 自动提供 `hideck_<版本>_openwrt_dynamic_<架构>` 及其 SHA256 文件。
它保留动态加载能力，但不会被 IPK/APK 打包流程或 `deploy-binary.sh` 自动选择，
避免给不需要录音的设备增加运行库要求。设备需安装 `libgcc` 和 `lame-lib`
（使用 `opkg install` 或 `apk add`），并核对 Release 中的 SHA256SUMS。发布流程
使用 [sdk-matrix.json](sdk-matrix.json) 固定的 OpenWrt 24.10 SDK 构建三个架构，
不使用只能生成 static PIE 的通用 ARM 交叉工具链冒充动态产物。

发布流水线和 SDK 打包镜像使用 HTTPS 上的 HTTP/1.1 下载官方 SDK，避免大型下载中
观察到的 HTTP/2 流重置。统一使用 `download_sdk.py`：连接超时 20 秒，单次传输
上限 600 秒，最多 6 次尝试，共用 3600 秒传输预算（发布构建机曾持续只有约
80 KB/s，超过 200 MB 的 SDK 需要约 45 分钟）。可恢复的传输错误间隔 5 秒重试，
通过 HTTP Range 从 `.part` 断点续传，不从头重复下载。HTTP 错误、证书错误、
服务器不支持续传或 SHA-256 不符会明确失败；未校验的文件不会解压或发布。

GitHub Actions 只缓存完整且通过固定 SHA-256 校验的 SDK，每次命中仍重新校验；
缓存受 GitHub 的分支/tag 访问范围约束。后续制包任务通过 Docker named context
复用同一 SDK，不在容器内重复下载。单独构建 SDK 镜像时，BuildKit cache mount
保留已验证文件和中断下载，重新执行相同构建可继续下载。校验失败需要人工检查并
清理对应坏缓存，不会悄悄跳过。缓存不包含 HiDeck 配置或凭证。

本地也可直接调用：

```sh
python3 packaging/openwrt/download_sdk.py --url "$SDK_URL" \
  --sha256 "$SDK_SHA256" --cache-dir /path/to/sdk-cache
```

使用同一目录可续传，指定新目录可禁用旧缓存；`--attempts 1` 关闭自动重试，
`--attempt-seconds` 和 `--total-seconds` 可调整超时。同一缓存目录不要并发运行脚本。

`build.sh` 仍支持 `LINK_MODE=dynamic`，可用与设备固件匹配的 OpenWrt SDK
自行构建。以下方式用于 Release 没有覆盖的目标或自定义固件：

以下以 Linux 构建机、OpenWrt 25.12.2 x86/64 SDK 为例，不使用 Docker。
先将对应版本 SDK 解压到本地，然后在仓库根目录执行：

```sh
npm ci --prefix web
npm run build --prefix web
rm -rf internal/web/dist
cp -R web/dist internal/web/dist

SDK_DIR=/path/to/openwrt-sdk-25.12.2-x86-64
export STAGING_DIR="$SDK_DIR/staging_dir"
export CC="$STAGING_DIR/toolchain-x86_64_gcc-14.3.0_musl/bin/x86_64-openwrt-linux-musl-gcc"
GOARCH=amd64 LINK_MODE=dynamic VERSION=v2.1.24+dynamic \
  OUT=out/hideck-openwrt-dynamic sh packaging/openwrt/build.sh
```

其他固件/架构应使用自己的 SDK 和编译器路径。脚本检查动态产物的解释器
是 `/lib/ld-musl-*`，避免把构建机的 glibc 程序当作 OpenWrt 程序。
部署前检查 `readelf -d out/hideck-openwrt-dynamic` 中的 `NEEDED`；
本次 x86/64 产物依赖系统 musl 和 `libgcc_s.so.1`。在设备安装 `libgcc`
和 `lame-lib`，然后在无活动通话时备份现有程序并替换实际 procd 服务
使用的二进制。配置、数据库和账号不需要迁移；不要在通话中替换或重启服务。

PCMU 模组直拨的 MP3 编码已验证；AMR 实时编解码仍需对应库。
详细音频修复与验证范围见 [模组直拨排障](../../docs/modem-voice-troubleshooting.md)。

## 本地 SDK 构建

需要 Docker（SDK 构建主机为 Linux x86_64）和已经构建好的对应架构 musl 静态 HiDeck。先构建前端，再用 [build.sh](build.sh) 构建二进制；不能用普通 `CGO_ENABLED=0 go build` 代替静态链接流程。

以下以 24.10.8 x86/64 为例，输入放在仓库 `input/` 下，输出使用空目录：

```sh
target=$(jq -c '.include[] | select(.id == "24.10.8-x86-64")' packaging/openwrt/sdk-matrix.json)
SDK_URL="https://downloads.openwrt.org/releases/$(printf '%s' "$target" | jq -r .release)/targets/$(printf '%s' "$target" | jq -r .target)/$(printf '%s' "$target" | jq -r .sdk)"
SDK_SHA256=$(printf '%s' "$target" | jq -r .sha256)
docker build --platform linux/amd64 -f packaging/openwrt/Dockerfile.sdk \
  --build-arg SDK_URL="$SDK_URL" --build-arg SDK_SHA256="$SDK_SHA256" -t hideck-openwrt-sdk .
mkdir -p openwrt-packages
docker run --rm --platform linux/amd64 \
  --mount "type=bind,source=$PWD,target=/src,readonly" \
  --mount "type=bind,source=$PWD/openwrt-packages,target=/out" \
  -e SDK_DIR=/sdk -e HIDECK_VERSION=v2.1.23 -e OUTPUT_DIR=/out \
  -e HIDECK_BINARY=/src/input/hideck_v2.1.23_openwrt_amd64 \
  hideck-openwrt-sdk timeout 3500 sh /src/packaging/openwrt/build-packages.sh
```

脚本验证 ELF 架构与静态链接、计算并校验输入哈希，使用 SDK 固定的 feeds revision 编译 ADB，仅收集三个 HiDeck 包。主包只封装已经编译好的静态程序，不重复构建 QMI/内核组件；包元数据仍声明这些依赖，由目标系统的 opkg/apk 检查并安装。输出带 SHA256SUMS；APK 额外签名并验证。标准库和内核依赖从目标系统匹配的软件源安装，不随附件混装。

二进制发布工作流自动调用 [openwrt-packages.yml](../../.github/workflows/openwrt-packages.yml)，使用同次构建的 HiDeck，避免把尚不识别专用 ADB 路径的旧版本包装进来。也可手动触发 **Build Release Binaries** 从当前源码构建整套产物；源码提交和二进制版本会写入 `BUILDINFO`。`dev` 构建的包管理版本为 `0.0.0`，程序内版本仍为 `dev`；正式发布使用数字版本。
