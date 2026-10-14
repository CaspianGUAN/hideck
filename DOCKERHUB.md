# HiDeck Docker Hub 镜像

镜像地址：`yibaiba/hideck`

支持架构：

- `linux/amd64`
- `linux/arm64`

## 快速启动（推荐）

直接通过 curl 运行部署脚本，默认安装到当前目录下的 `hideck/`：

```bash
curl -fsSL https://raw.githubusercontent.com/yibaiba/hideck/main/deploy.sh | sh
```

自定义安装目录：

```bash
curl -fsSL https://raw.githubusercontent.com/yibaiba/hideck/main/deploy.sh | HIDECK_DIR=/opt/hideck sh
```

脚本会下载 `docker-compose.yml` 和配置模板，创建持久化目录并拉取 `latest`；不会覆盖已有的部署文件和 `config/config.yaml`。

## 手工部署

```bash
mkdir -p hideck/{config,data,logs}
cd hideck
```

创建 `config/config.yaml`：

```yaml
server:
  port: 7575
  debug: false
  https_enabled: false

web:
  username: admin
  password: admin

devices: []

proxy:
  instances: []

vowifi:
  enabled: false
```

创建 `docker-compose.yml`：

```yaml
services:
  hideck:
    image: yibaiba/hideck:latest
    container_name: hideck
    restart: unless-stopped
    init: true
    stop_grace_period: 30s
    network_mode: host
    privileged: true
    volumes:
      - ./config:/app/config
      - ./data:/app/data
      - ./logs:/app/logs
      - /dev:/dev
    environment:
      TZ: Asia/Shanghai
      CONFIG_PATH: /app/config/config.yaml
    logging:
      driver: json-file
      options:
        max-size: 10m
        max-file: "3"
```

启动：

```bash
docker compose up -d
```

Web 入口：`http://YOUR_IP:7575`

默认账号：`admin` / `admin`

首次登录后请立即修改密码。

<a id="module-voice-dependencies"></a>

## 模组直拨依赖

飞牛 fnOS 应用中心的 `.fpk` 打包、权限说明及验证边界见[飞牛应用包](packaging/fnos/README.md)。包内复用本镜像，不另行修改电话或 VoWiFi 协议。

四种镜像构建路径都安装 ADB 和 `alsa-utils`（提供 `arecord`、`aplay`），并在构建时检查程序可执行以及 ADB 的 `-t` / `-L` 能力。已适配固件 `QDC507GLEFM21` 使用的模组端驱动和音频桥接程序内嵌在 HiDeck 二进制中，不需要首次联网下载，也不需要另行挂载 `data/modem-voice/bundles/`。

容器不提供宿主机内核驱动。Linux 宿主机需要内置或已安装 `snd_usb_audio`；可在宿主机检查：

```sh
test -d /sys/bus/usb/drivers/snd-usb-audio || modinfo snd_usb_audio
```

如果检查失败，先安装与**当前运行内核**匹配的 USB 音频驱动；OpenWrt 对应 `kmod-usb-audio`。不要强行安装其他内核版本的模块。不插模组时没有 `/dev/snd` 不代表驱动缺失。

保留默认 Compose 的 `privileged: true` 和 `/dev:/dev`：切换 USB 配置或重启模组后，ADB、串口及 PCM 设备节点会变化，不能只绑定某一个旧设备节点。ADB 工具已在镜像内，不要求宿主机另起 ADB 服务。自定义 Compose 需要保留这些设备访问条件。

Linux 二进制部署脚本也会安装上述用户态依赖，并检查宿主机驱动；不支持的 ADB、包安装失败或驱动无法确认会明确报错。OpenWrt 默认不装语音依赖，新版专用 ADB 和可选语音包见 [OpenWrt 安装说明](packaging/openwrt/README.md)。这些依赖检查不修改 USB 配置、不切换通话模式，也不重启模组；模组自动开启 ADB 仍只在用户选择模组直拨时执行。

## 维护者发布

发版镜像不再在 Docker 里编译应用。先打好 `dist/hideck_vX.Y.Z_linux_amd64` 和 `linux_arm64`；`Dockerfile.release` 使用 Debian，并自行安装运行依赖，再复制二进制。

原来的源码构建还在：根目录 `Dockerfile` + `docker-compose.source.yml`，运行层使用 Alpine。`hideck-runtime` 是独立的 Alpine 底包，重建它不会自动更新 Debian 发版镜像；两条路径的依赖需同步维护。

独立运行时底包（ADB、ALSA、`ca-certificates`、AMR/MP3、`gcompat`、`qmi-proxy`）在依赖变化时重建：

```bash
docker compose -f docker-compose.runtime.yml build --builder hideck-release --push
```

arm64 拉 Alpine 包若 TLS 失败，改用：

```bash
docker buildx build --builder hideck-release --allow network.host \
  --platform linux/amd64,linux/arm64 -f Dockerfile.runtime \
  -t yibaiba/hideck-runtime:3.24 -t yibaiba/hideck-runtime:latest --push .
```

每次发版：

```bash
# 先 make / 本地编出 UPX 后的 dist/hideck_v2.1.23_linux_amd64 和 linux_arm64
export HIDECK_VERSION=2.1.23
export HIDECK_MINOR_VERSION=2.1
export HIDECK_REVISION="$(git rev-parse HEAD)"
export HIDECK_BUILDTIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"

docker compose -f docker-compose.build.yml build --builder hideck-release --push
docker buildx imagetools inspect "yibaiba/hideck:${HIDECK_VERSION}"
```

服务器部署仍只用 `docker-compose.yml` 拉 `yibaiba/hideck:latest`，不会在服务器编译。

从源码完整构建（更新依赖或不用预编译二进制）：

```bash
export HIDECK_VERSION=2.1.23
export HIDECK_MINOR_VERSION=2.1
export HIDECK_REVISION="$(git rev-parse HEAD)"
export HIDECK_BUILDTIME="$(date -u +'%Y-%m-%dT%H:%M:%SZ')"
docker compose -f docker-compose.source.yml build --builder hideck-release --push
```

## 更新镜像

```bash
docker compose pull
docker compose up -d
```

应用内二进制热更新在这个源码整合构建中已禁用。Docker 部署请通过拉取新镜像升级。

## 配置说明

| 路径 | 说明 |
| --- | --- |
| `/app/config` | 配置文件目录 |
| `/app/data` | SQLite 数据与运行数据 |
| `/app/logs` | 日志目录 |

容器默认时区为 `Asia/Shanghai`。Compose 文件也显式设置了同一时区，方便在不同运行方式下保持一致。

## 许可证提示

本仓库是源码整合树，不是单一 MIT 许可项目。根项目来自 PolyForm Noncommercial 1.0.0，`third_party/vowifi-go` 为 AGPL-3.0，其它第三方源码按各自许可证授权。发布公开二进制或 Docker 镜像前，请先确认组合分发的许可证义务。

## PC/SC smart-card readers

HiDeck supports PC/SC readers without a cellular modem. The Docker images include
only the PC/SC client library; the Linux host owns the USB reader through `pcscd`
and its reader driver. Passing `/dev` alone does not expose this service.

On Debian/Ubuntu, install and start the host dependencies:

```sh
sudo apt-get update
sudo apt-get install -y pcscd libccid pcsc-tools
sudo systemctl enable --now pcscd.socket
sudo systemctl start pcscd.service
sudo pcsc_scan
```

Confirm that the reader and inserted card appear, then exit `pcsc_scan` with
Ctrl-C. Some readers need a vendor driver instead of `libccid`. On other Linux
distributions use the equivalent packages/service; the commands above assume
systemd. Do not run a second `pcscd` in the container against the same reader.

After the normal Docker installation, save `docker-compose.pcsc.yml` from this
repository alongside your existing `docker-compose.yml`, then recreate HiDeck:

```sh
curl -fsSL https://raw.githubusercontent.com/yibaiba/hideck/main/docker-compose.pcsc.yml \
  -o docker-compose.pcsc.yml
test -S /run/pcscd/pcscd.comm
docker compose -f docker-compose.yml -f docker-compose.pcsc.yml up -d
```

The override mounts `/run/pcscd` read-only. Unix socket connections still work;
mounting the directory instead of the socket allows `pcscd` to recreate it after
a restart. The directory must already exist (`create_host_path: false`); a missing
host service should not silently create an empty directory. Ensure `pcscd` is
started before HiDeck after a host reboot. If your distribution uses a different
socket directory, adjust the bind source and check the client socket path.

Use the same `-f` arguments for subsequent `pull`, `up`, and `logs` commands.
Include any existing Caddy overrides as well. The one-click `deploy.sh` preserves
existing files but does not automatically select this optional override; rerunning
it alone will omit the PC/SC mount. A plain modem-only installation does not need
this override or a running host `pcscd`.

In HiDeck, discover/add the physical PC/SC reader, then read its EID/profile list.
A blank eUICC may return no profiles. This verifies card access, not carrier
activation or VoWiFi registration. If no reader appears, check host `pcsc_scan`,
the bind mount, and host `pcscd` logs/access policy. Keep other SIM applications
from holding the card during operations; do not disable host access controls as
a workaround.
