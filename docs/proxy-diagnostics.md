# 前置代理出口检测

代理页分别展示三类信息：

- **SIM 国家规则**：按 SIM 的归属 MCC 选择代理，不是代理所在国家，也不要求出口与 SIM 同国。
- **公共 DNS UDP**：验证 SOCKS5 UDP Associate 和实际 DNS UDP 数据往返；不能替代运营商 ePDG/IKE 建链验证。
- **实测出口（HTTPS）**：通过所配置的 SOCKS5 请求 `https://www.cloudflare.com/cdn-cgi/trace`，读取 `ip` 和 `loc`。不会使用 `colo`（Cloudflare 机房）作为出口国家。

HTTPS 请求使用该代理的认证信息，不会在失败时改为直连。出口 IP 支持 IPv4 和 IPv6，显示的是本次 HTTPS 请求实际观察到的地址；它不代表同时检测了两个地址族，也不能证明 UDP/IKE 流量使用相同出口。代理按目标地址或协议分流时，两者可能不同。

代理页默认每分钟检测一次，可取消“每分钟自动检测”，或点击刷新手动检测。离开代理页面后停止轮询。这不是服务端全天候监控。

当前页面会话中发现 IP 或国家变化时，会显示前后值和时间。后续失败时，当前出口显示“未确认”，旧结果仅作为带时间的“上次成功”信息。浏览器重新加载、代理配置更新或禁用后，旧观测基线不会继续沿用。

没有上游节点、SOCKS5 认证失败、转发链路中断以及检测服务本身故障都可能造成探测失败，不能单凭一个超时判断具体原因。诊断错误会显示在对应项目中；未知国家不会伪装成配置规则中的国家。

出口检测用于提示，不会自动修改国家规则、禁用代理、切换正在使用的 VoWiFi 会话，或替代现有恢复逻辑。检测服务故障不会阻止原有 ePDG 尝试。

国家归属采用 Cloudflare 的 IP 地理数据库，可能与其他数据库不同。参考：[诊断端点](https://developers.cloudflare.com/fundamentals/reference/cdn-cgi-endpoint/)、[IP 地理位置准确性](https://developers.cloudflare.com/network/ip-geolocation/)。
