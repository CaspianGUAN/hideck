# 运营商实测与预设备注

记录 2026-08-18 会话结论，避免和「能上网」「手机能收短信」混在一起。

## EC25 和短信 / 电话

- 这块现场模组是 `2c7c:0125` Quectel EC25（大疆改身份一类）。QMI WMS、NAS、VOICE 能分配；原生 IMS/IMSA/IMSP 分配失败（`qmi service not supported by hardware`）。
- **上网**：EC25 本职，QMI 拨号即可。别人改模组上网走的是这条，不是 VoLTE。
- **国内卡驻自家网**：短信可以走原版驻网（关软件电话、射频开、QMI/AT）。国内 VoLTE / 公网 VoWiFi 打电话，这块 EC25 做不了可靠方案。代码里 MCC `460`/`461` 会直接拒绝 VoWiFi。
- **手机能收、棒子驻网收不到**：认证手机有 VoLTE/IMS 短信；EC25 关掉软件 IMS 后只剩 QMI。英国卡漫游中国移动时，刚才实测 WMS 发送超时、入站没有 URC。

## giffgaff（234/10）

- 有完整预设 `giffgaff_23410`。WiFi calling（关射频 + 软件 IMS）能 REGISTER 200、`contact_smsip=true`。
- 关射频时发 `INFO`→`85075`：O2 IP-SM-GW 回 **RP-ERROR 69 facility not implemented**。短号 IMS 被网元拒，不是没注册。
- 切成「蜂窝 + on_demand」会空闲拆隧道（`VOWIFI_DESIRED_RECOVER_SKIPPED_CELLULAR_IDLE`），此时既没有 IMS 也没有可靠的 QMI 入站。
- 原版驻网收短信：关 VoWiFi → 射频 Online → QMI 听/发，没有射频抑制、没有蜂窝空闲。原版没有另一套短信协议。
- 2026-08-18 在国内把 giffgaff 收成干净驻网（关 IMS、驻中国移动、不开流量）后：自发 `INFO`/自发短信 QMI 超时；手机发来的短信没有进模组。当时状态不是原版日常「从未开过 IMS 的驻网」。
- CTEUK 不要给 giffgaff 发短信（收费）。CTEUK 只测免费查询（`BAL`→`888`）和打电话。giffgaff 可以自己发 `INFO`。

## 英国沃达丰 / VOXI（234/15）

- VOXI 是 Vodafone Limited 的品牌，和沃达丰 UK 同一张网，IMSI 一般是 `23415…`。
- 原版 vohive / 当时的预设清单里 **没有** `vodafone_uk`，只有荷兰 `vodafone_nl_20404`。
- 现已加入预设 `vodafone_uk_23415`：标准 ePDG `epdg.epc.mnc015.mcc234.pub.3gppnetwork.org`，`device_model=rmx3366`，IKE/ESP 先用英国已通提案加宽列表，REGISTER 带 `smsip`。
- `23415` 的 port-s 恢复策略按 Vodafone UK IMS 网络生效，同时覆盖 Vodafone 和 VOXI：连接被对端明确 RST 后只等待 5 秒；运行超过 2 分钟的连接未恢复时先刷新当前 P-CSCF，REGISTER 成功后再切换一次 P-CSCF。真实 TCP 超时采用下面单独说明的验证后切换策略；普通 EOF 和其他运营商不套用该策略。
- 2026-09-06 的 179 日志中，port-s 在 16:22:12 超时，历史自动重连记录阻止了恢复调度，直到 16:39:14 周期注册后才重连。现将历史重连记录仅作为正常 EOF 的兼容依据（保留 2degrees 等网侧按需关闭行为）；超时、RST 和其他异常不跳过恢复，也不凭历史记录标记短信下行就绪。本地主动关闭不发起恢复。Vodafone UK/VOXI 普通异常保留 30 秒、明确 RST 保留 5 秒观察窗口；其他运营商的异常流关闭不增加这一固定等待，已有退避仍优先。普通 EOF 保留兼容观察，已观察过网侧自行重连时等待网侧；尚未观察到时维持原有 30 秒观察窗口。
- 2026-09-07 的 179 日志中，13:03 port-s 报 `operation timed out`，之后 8 次原节点 REGISTER 200 都未恢复下行。新增 `vodafone_uk_port_s_timeout` 策略：仅真实 TCP 超时，在 30 秒观察后刷新原节点，刷新成功再验证下行 30 秒；仍无下行时记录失败，现有随机退避到期后切换合格 P-CSCF。REGISTER 本身失败不因此启用切换，仍遵守原恢复规则。普通 EOF、本地读期限到期、主动关闭、其他运营商均不触发这条策略。gVisor 适配层保留真实传输超时与本地期限的区别，不用模糊错误字符串匹配决定换节点。此修改已做自动化回归，尚需真卡部署验收。
- 超时恢复期间的新 port-s 或当前受保护连接上的有效下行 SIP 请求可取消未执行的切换，旧连接的迟到消息不可取消。原连接的周期 REGISTER 200 不清除已建立的切换退避；验证失败后的迟到重连不会被当成按需重连能力。
- 完整重建后的替代会话也有独立下行验证期限：`IMS replacement registered; awaiting downlink validation` 会记录节点与截止时间。30 秒仍无下行只记为 `downlink_unverified`，不累加 REGISTER 失败次数；存在本轮未尝试的合格候选才继续替换。候选耗尽且注册传输仍可用时保留注册、监听器和安全通道，记录 `IMS downlink recovery round exhausted; keeping registration pending validation` 及轮次、`retry_at`，仍不宣告短信下行恢复。
- 下行验证的已尝试节点、轮次和重试期限跨 IMS/ePDG runtime 保留，重新获取相同列表或 UDP/TCP 回退不会开启新一轮。整轮结束后沿用首轮的随机观察间隔，不因被动观察轮次增加而指数增长：保留注册流时为 90～180 秒，无注册流时为 30～60 秒。这是 Vodafone 经验调度，不是 RFC 规定的下行建连时限。到期可重新尝试旧候选，单候选也可重新获取节点/重建。真实注册失败的指数退避和服务端 Retry-After 仍独立约束相应节点，可能要求等待更久。当前路径的迟到下行可取消等待；旧连接事件不可取消，新明确 RST/既有超时升级或当前路径 RP 回执 488 不受这轮空闲等待阻挡。普通 EOF、其他运营商不启用本策略。
- 注册前的异常恢复等待与 REGISTER 成功后的下行验证分开：通用异常可立即进入恢复，但注册后仍给异步建立的 port-s 独立验证窗口，不能因初始等待为零就马上判失败。该验证窗口默认 30 秒，是项目实现参数，不是 RFC 重试间隔。Vodafone UK/VOXI 的短信 RP 回执 488 仍直接请求重建并换节点，不受 port-s 观察窗口或其恢复退避阻挡；原路径降权 30 分钟，旧路径回执不会拆掉已切换的新路径，新节点注册失败仍遵守对应重试规则。
- 上述 30/5 秒为项目兼容策略，并非协议固定时限。恢复失败的重试继续遵循 [RFC 5626 §4.5](https://www.rfc-editor.org/rfc/rfc5626.html#section-4.5) 的随机指数退避和 Retry-After 下限；注册失败处理参考 [TS 24.229 §5.1.1.4.1](https://www.etsi.org/deliver/etsi_ts/124200_124299/124229/18.10.00_60/ts_124229v181000p.pdf)，不把反向 TCP 关闭直接等同于 IKE/IPsec 失效。
- 切换后的 REGISTER 200 只代表注册成功；新 port-s 建立或收到有效下行 SIP 请求后才算恢复完成。下行未验证的 P-CSCF 保留 30 分钟低优先级，但不由此禁止注册 30 分钟；共享恢复轮次与节点硬退避分别控制何时重试。自动化测试覆盖候选耗尽、跨 runtime 去重、到期恢复、迟到证据、Retry-After 和运营商隔离；仍需部署后结合真短信验收，停止频繁重建不等于短信已恢复。
- 当前注册 TCP 或 port-s 监听器明确失效时，取消已不存在连接的被动观察等待，不让它阻塞实际恢复；节点真实失败的重试期限仍保留。重叠重鉴权共享恢复状态时，取消操作还需匹配下行尝试编号，旧会话不能清除新会话的等待。监听器 `Accept()` 异常会报告本地错误并请求 runtime 恢复；清理后重新核对监听器归属，并与 REGISTER 完成串行处理，正常关闭和退役监听器的迟到错误不影响新会话。旧 IPsec 安装的清理回调只能移除自己的策略，不能删除后来安装的新策略。相关本地问题已用回归测试复现，不代表已证明它们就是 179 本次缺少下行的原因。
- `IMS downlink path diagnostics` 在注册成功、替代路径验证到期、监听失败时输出安全元数据；现有 `IMS inbound stats` 和状态 diagnostics 也包含 `network`。用户态网络记录外层 `inner_endpoint`、包桥读入/转换/写出错误、当前 IPsec 的地址、SPI、端口，以及 `flow_c` / `flow_s` 的 SYN、SYN-ACK、ACK、RST、重放/解密失败计数。`outbound_protected` 只代表本地完成加密，必须同时核对包桥写出与对端 ACK；计数不是端到端短信成功证明。无 SYN 也不能仅凭该计数认定网侧没发送，应同时看隧道和解密错误。系统网络或外部传输没有这些诊断时显示 `available: false`。不保存报文正文、短信内容或密钥。
- 国家代理仍按 MCC `234` 走 GB 规则。VOXI 官方写明漫游不支持 WiFi calling，人在国内需走代理。
- 真卡验收前：IKE/ESP 仍可能要按日志改。余额查询未做自动短码（走 App）。

## Lebara UK NextGen（234/87）

- 分享 eSIM，LPA 显示名常见 `Lebara UK`，有的列表会写成 `0 Lebara UK`（前面的数字是序号，不是 PLMN）。认卡靠 IMSI `23487…`，不要靠卡名里的 `0`。
- 不是旧的 Vodafone-hosted Lebara（234/15 + GID 90）。234/15 仍走 `vodafone_uk_23415`。
- 自有 MNC `87`，宿主网仍是 Vodafone UK。预设 `lebara_uk_23487`：标准 ePDG `epdg.epc.mnc087.mcc234.pub.3gppnetwork.org`，`device_model=rmx3366`，IKE/ESP 先用英国已通提案加宽列表，REGISTER 带 `smsip`。
- 双 IMSI：一驻国内 460 常会切到 `20404`。切过去之后不要当荷兰沃达丰去连；英国 WFC 不可用。分享卡运行时锁射频、拒绝开网络/关飞行/蜂窝模式，避免踩这条路。
- `20404` 不是 Profile 损坏。同一 ICCID 做一次 Disable→Enable（或经停车 Profile 中转）可以把活动身份拉回 `23487`，不必删卡重写。VoHive 在活 IMSI 变成 `20404` 时自动清污，连续 3 次读到 `23487` 后再开 VoWiFi。射频锁不变。模组重启本身清不掉。
- `20404` 且没有 Lebara 证据时，仍是 `vodafone_nl_20404`。
- 国家代理按活 IMSI 的 MCC：`23487` 走 GB。人在国内必须走英国代理。
- 真卡验收前：IKE/ESP 仍可能要按日志改。余额查询未做自动短码（走 Lebara App）。

## Spark NZ（530/05）

- 预设在首次 IKE_AUTH 中不主动发送设备身份；如果网侧明确请求，则在网络认证通过后回复可用的 IMEI。已建立的 IKE SA 上也支持 INFORMATIONAL 设备身份请求。
- 预设在 CHILD_SA rekey 收到 `NO_ADDITIONAL_SAS` 时保留当前 CHILD_SA。这是 Spark 兼容策略，不是所有运营商的默认行为。
- 两个开关均可通过 `config/carrier_overrides.yaml` 显式关闭；不填写则继承预设。修改后需重启服务并重新建立会话。例如：

  ```yaml
  carrier_overrides:
    "53005":
      withhold_device_identity: false
      keep_child_sa_on_rekey_decline: false
  ```

- 自动化测试覆盖身份请求编码、认证门控、发送失败和配置覆盖；不替代 Spark、VOXI 或其他运营商的真卡验收。

## 德国 O2（262/03、262/07）

- 预设已有：`O2_de_26203`、`O2_de_26207_alias`。ePDG 用 3GPP 名 `epdg.epc.mnc003.mcc262.pub.3gppnetwork.org` / `mnc007`。
- IKE 先出 `aes256-sha256-prfsha1-modp2048`（iPhone 侧已通过的提案），设备身份 `iphone15,4`。
- 国家代理按 MCC `262` 走 **DE**。人在国内要配德国前置，和 GB 同一套规则表。
- 余额查询码按套餐不同（`*105#` / `*101#`），命令中心标成不支持自动查。

## 荷兰 Vodafone（204/04）

- 预设已有：`vodafone_nl_20404`。ePDG `epdg.epc.mnc004.mcc204.pub.3gppnetwork.org`。
- **不要**把 Lebara UK 切到 `20404` 的分享卡当成这家。光秃 204/04 且没有 Lebara 证据时才走这条。
- 国家代理按 MCC `204` 走 **NL**。
- 余额短码：`STATUS` → `4000`。

## 菲律宾 DITO（515/66）

- 新增预设 `dito_51566`。标准 ePDG `epdg.epc.mnc066.mcc515.pub.3gppnetwork.org`，`device_model=rmx3366`，IKE/ESP 用宽列表，REGISTER 带 `smsip`，403/500 允许 fallback。
- DITO 公开页只保证 VoLTE；VoWiFi 按机型开通，白名单不公开。真卡 IKE/REGISTER 仍可能要按日志改提案或 `device_model`。
- 国家代理按 MCC `515` 走 **PH**。人在国内需配菲律宾前置。
- 余额走 DITO App / `*143#` 菜单，没有可审计的统一免费短信码。

## 市面常见 eSIM（按 home PLMN，不是品牌名）

Airalo / Holafly / Nomad 一类旅行流量卡通常没有 IMS 开户。即使宿主 IMSI 落在下面这些 PLMN，REGISTER 仍会被拒。下面加的是带号码、官方写过 WiFi calling 的宿主网。匹配键是 SIM 的 home MCC+MNC，不是包装上的品牌。

IKE/ESP 先用宽列表，`device_model=rmx3366`，REGISTER 带 `smsip`，403/500 允许 fallback。真卡验收前仍可能要按日志改提案或设备身份。

| 运营商 | PLMN | 预设 | 国家前置 |
| --- | --- | --- | --- |
| 1GLOBAL / 原 Truphone | 234/25 | `oneglobal_23425` | GB |
| Lycamobile UK | 234/26 | `lycamobile_uk_23426` | GB |
| EE UK | 234/30、234/31、234/32 | `ee_uk_23430` / `23431` / `23432` | GB |
| Orange France | 208/01 | `orange_fr_20801` | FR |
| Telekom Germany | 262/01 | `telekom_de_26201` | DE |
| Vodafone Germany | 262/02 | `vodafone_de_26202` | DE |
| 中国移动香港 CMHK | 454/12、454/13 | `cmhk_45412` / `cmhk_45413` | HK |
| 乌龟卡 / TravelSIM / Elisa | 248/02 | `elisa_ee_24802` | EE |

- EE 的 234/33 仍是 `CTEUK_23433`，不要当成 EE 官方卡。
- Lycamobile UK 是 234/26。美国 Lyca 走 AT&T 的 `310/410`，两套不要混。
- 1GLOBAL 若 DNS 不到标准 3GPP ePDG，再按 IKE 日志改自定义主机名。
- 人在国内必须配对应国家前置：GB / FR / DE / HK / JP / EE / NL / MY / PH / TH / NG。UI 里还没有该国规则时，WFC 连不上 ePDG。
- 余额查询资料不足，命令中心标成不支持自动查。

香港 MCC `454` 的 PANI 国家码现在是 **HK**（以前掉进默认 `XX`）。CSL / 3HK / CMHK 都会用到。

## 日本（440）官方 Wi-Fi通話

三大运营商官方套餐和线上品牌 eSIM 有 3GPP Wi-Fi通話。匹配仍按 home PLMN。IIJmio / mineo 一类 MVNO、以及日本旅行流量卡，通常没有 IMS 开户。

| 运营商 | PLMN | 预设 | 常见 eSIM |
| --- | --- | --- | --- |
| NTT Docomo | 440/10 | `docomo_44010` | ドコモ官方 eSIM |
| SoftBank / LINEMO | 440/20 | `softbank_44020` | SoftBank、LINEMO |
| Y!mobile | 440/00 | `ymobile_44000` | 旧 eAccess IMSI；新卡常见 440/20 |
| KDDI au / povo / UQ | 440/51 | `kddi_44051` | au、povo、UQ VoLTE 卡 |

- 没加 440/50（非 VoLTE）、440/52（IoT）、440/53（乐天漫游 KDDI）、Starlink / JPN-ROAM 号段。
- **乐天 440/11 不加**。语音走 Rakuten Link，不是 ePDG VoWiFi。
- 国家代理按 MCC `440`/`441` 走 **JP**。人在国内需配日本前置。
- ePDG 先用标准 3GPP 名。Y!mobile `440/00` 若 DNS 不到，再按日志看要不要改 SoftBank `mnc020`。
- 余额走各家 App，命令中心标成不支持自动查。

## 乌龟卡 / 爱沙尼亚 Elisa（248/02）

市面「乌龟卡」是 esim.gg / Nekoko Telecom，TravelSIM 的代理，+372 号码，宿主网是 **Elisa Eesti 248/02**，不是英国卡，也不是中国移动。

- 新增预设 `elisa_ee_24802`。标准 ePDG `epdg.epc.mnc002.mcc248.pub.3gppnetwork.org`，`device_model=rmx3366`，IKE/ESP 用宽列表，REGISTER 带 `smsip`，403/500 允许 fallback。
- Elisa 官方页写明商务套餐有 VoWiFi。TravelSIM/乌龟卡是 MVNO，IMS 开户不保证；没开户时 IKE/REGISTER 会被拒。
- 国家代理按 MCC `248` 走 **EE**。人在国内需配爱沙尼亚前置。
- 248/02 也是 Elisa 官方卡。余额短码不能自动选：乌龟卡/TravelSIM 文档有 `*146*099#`，Elisa 官方走账户页。

## 本轮核对过的 eSIM

| 品牌 | 结论 | 原因 |
| --- | --- | --- |
| Lycamobile UK | 已有 | `lycamobile_uk_23426` |
| 新西兰 2degrees | 已有 | `2degrees_nz_53024` |
| CMLink UK | 不另加 | 宿主 EE `234/30`，客服多次说不支持 WFC；IMSI 已走 EE 预设 |
| Asda Mobile | 不另加 | 官方有 WFC，宿主 VF `234/15`，且没有 eSIM |
| Saily | 不加 | 流量卡 + App 虚拟美号，不是 ePDG VoWiFi |
| 克罗地亚 A1 | 不加 | A1 HR 没有 VoWiFi；旅游 eSIM 偏流量 |
| 格鲁吉亚 Cellfie | 不加 | 只公开 VoLTE，没有 VoWiFi |
| 摩尔多瓦 eSIM | 不加 | 旅行卡多是流量；Orange/Moldcell 没有可审计的 VoWiFi |
| Hotlink MY | 新增 | 官方 VoWiFi，`hotlink_my_50212`（502/12） |
| AIS TH | 新增 | 官方 Wi-Fi Calling，`ais_th_52001` / `52003` |
| Smart PH | 新增 | 已商用 VoWiFi，`smart_ph_51503` |
| GlobeOne / Globe | 新增 | 官方 VoWiFi，`globe_ph_51502` |
| Simyo NL | 新增 | 官方 wifi bellen，宿主 KPN `kpn_nl_20408` |
| MTN 尼日利亚 | 新增 | 有 eSIM 和 VoWiFi 报道，`mtn_ng_62130` |

Hotlink 官方写明**漫游时 VoWiFi 不可用**。人在国内仍先走 MY 前置试 ePDG；账户没开国际 WFC 时会被拒。

尼日利亚 MCC `621` 的国家码是 **NG**。

## 英国侧已有预设

| 运营商 | PLMN | 预设 |
| --- | --- | --- |
| giffgaff | 234/10 | `giffgaff_23410` |
| Vodafone UK / VOXI | 234/15 | `vodafone_uk_23415` |
| Three UK | 234/20 | `three_uk_234020` |
| 1GLOBAL / Truphone | 234/25 | `oneglobal_23425` |
| Lycamobile UK | 234/26 | `lycamobile_uk_23426` |
| EE UK | 234/30、31、32 | `ee_uk_23430` 及别名 |
| CTExcel | 234/33 | `CTEUK_23433` |
| Lebara UK NextGen | 234/87 | `lebara_uk_23487` |
