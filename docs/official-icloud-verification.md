# 官方 Windows iCloud 验证排查

排查日期：2026-10-06。本机 iCloud 15.8.118.0。

## 原生接口

安装包类型库包含 `INCAccount`，GUID 为 `02B21C08-C2CD-4698-A72F-29F2A6403851`。清单注册的 COM 类为 `NCAccount`，GUID 为 `C5B12896-DBB7-4419-85E0-9C6D6F51E64E`。

旧 `AccountInfo` 类虽然出现在类型库里，却未被 Store 包注册，激活返回 `80040154`。

反汇编确认：`NCAccountImpl::ShowAuthDialog` 调用旧 `AOSAccountShowAuthDialog`。测试返回 AOS 错误 `5002`，不能把它当作界面的 AuthKit 验证入口。`ShowTOSDialog` 实验返回 `80010105`，这条入口尚不能自动完成条款。

界面的“验证 Apple 账户”进入另一条内部 AuthKit 流程。项目的 `refresh` 不等价于此流程。

## 本轮实测

- 01:45:53（UTC+8）：原生刷新请求返回 `MOBILEME_TERMS_OF_SERVICE_UPDATE`。
- 01:52:40（UTC+8）：保留缓存并重启官方主界面后，日志记录 `TOS not accepted. Retrieving TOS info...`，官方界面实际显示条款窗口。
- 用户随后勾选条款，但观察到“同意”按钮仍灰色，条款窗口未关闭；01:59:04 的刷新仍被条款要求拒绝。
- `INCAccount.CopyAuthInfo` 能返回储存服务的认证凭据。只读探针请求缓存的 `getManageCloudStorage` 地址得到 HTTP 401。探针未添加官方的 anisette 头，因此该结果不等价于完整 AOSKit 储存请求，不能单独证明凭据过期。
- 02:03:57：已通过进程级环境变量让官方主界面使用 Microsoft 签名有效的 WebView2 154.0.4258.53；其实际子进程路径已核对。原捆绑运行时为 105。新运行时使用单独页面缓存，尚未验收账户验证或购买页面恢复。

此前储存接口的 HTTP 401 与购买页脚本异常是不同请求。尚未抓到购买页面 HTTP 400 的完整请求链，不能声称已确认它的根因。

## 工具与结果含义

- `inspect-icloud-interfaces.ps1` 使用 `LoadTypeLibEx(REGKIND_NONE)` 读取本机接口，不执行 Apple 二进制代码，也不修改注册信息。
- `verify-official-icloud.ps1` 是旧 COM 接口与只读储存请求的实验探针。返回 `ok` 只表示对应调用的结果，不能证明 AuthKit 验证、加密信任或订阅成功。`SessionFile` 仅检查所选账户缓存是否存在，不证明它是官方当前账户。
- `diagnose-icloudplus.ps1` 输出带真实事件时间的分类元数据，不输出敏感原始行。
- `start-icloud-modern.ps1` 仅覆盖本次官方主界面的 WebView2 运行时和页面缓存，不修改系统或 Apple 的认证缓存。它不会代为购买。

## 公开实现参考

- [OpenBubbles/rustpush](https://github.com/OpenBubbles/rustpush/blob/master/src/auth.rs)：有自己的 delegate 交换、条款处理和储存信息读取，不能直接恢复 Windows 官方 AuthKit 缓存。
- [picklepete/pyicloud](https://github.com/picklepete/pyicloud/blob/master/pyicloud/base.py)：覆盖网页账户会话及双重验证，不能据此认为 Windows 官方会话已恢复。
- [Microsoft WebView2 环境参数](https://learn.microsoft.com/en-us/microsoft-edge/webview2/reference/win32/webview2-idl)：`WEBVIEW2_BROWSER_EXECUTABLE_FOLDER` 和 `WEBVIEW2_USER_DATA_FOLDER` 的来源。

## 本轮储存 UI 反汇编结果

`iCloudHome.exe` 15.8.118.0 的静态字符串和 `.pdata` 函数边界确认，储存窗口在
`0x1401fc9a0` 初始化 Web UI，向页面注入 `appleId`、`prsId`、`authToken`、
`deviceUDID`、`upgrade`、`manage` 和 `referringContext`，并提供
`refreshAccountQuota`、`getUserDSID`、`openExternalLink` 等回调。
配额读取入口位于 `0x1400ee120`，解析函数 `0x1400ed9c0` 读取
`storage_data`、`quota_info_in_bytes`、`total_quota`、`paid_quota` 和
`storage_usage_by_media`。这支持项目自己的只读储存查询实现，但没有证明
`volumeAuthenticate` 的购买请求可以脱离 AuthKit/条款状态直接提交。

项目现在只使用 delegate 返回的储存端点做 HTTPS 只读查询；不会调用这些 Web UI
回调，也不会自动提交升级或付款请求。

本轮代码增加了 `storage-plan` 只读动作：它只在 delegate 的
`storagePlanURL` 上发起 GET，并把 Apple 返回的容量、地区、币种和方案价格
做脱敏结构化展示。反汇编中 `StorageUIWnd` 还会读取 `buyQueryParam` 并把它交给
购买页面，但未发现可独立复用的付款提交函数；因此该参数不能被当成已经建立的
购买会话，也不能据此自动扣款。

面板的“提交 50GB / ¥6”动作现在会先强校验容量、币种、价格和周期，然后按
WebView 实际使用的链路请求 `presentOffers`、选取精确的 `buyProduct` 隐藏字段，
再提交 `volumeAuthenticate` 登录表单。结果会区分 `completed`、
`confirmation-required` 和 `handoff`；只有页面明确返回成功标记才会报告购买完成。
认证或付款失败只返回阶段和 HTTP 状态，不会把登录密码、Cookie、authToken 或完整
购买 URL 写入日志。当前 Windows 安装未包含可接管 Apple Store 最终确认的客户端，
因此收到 `confirmation-required`/`handoff` 时不能声称订阅已完成。

本机新后台的实测结果：`presentOffers` 已成功返回 CN 方案，解析到 50GB / CNY
600 分；随后 `volumeAuthenticate` 返回 HTTP 400。项目现在把它报告为
`purchase-authentication` 并保留登录会话，不会把这次尝试写成购买成功。这个状态
说明 Apple 的 Store 认证仍拒绝当前请求，不能通过猜测表单或绕过付款确认来修复。

代码排查还确认了一个此前被丢弃的认证材料：GrandSlam 的 SPD/token 集合会下发
`com.apple.gs.icloud.storage.buy`。这是 iCloud+ 商务专用服务令牌，不能用
`mmeAuthToken` 代替。项目将它附加到只读 Store 方案和 `buyProduct` 导航请求；
`volumeAuthenticate` 页面本身按 WebView 成功控件顺序提交，不额外伪造 SAP 签名。
修复后的二进制已重新构建并通过全量 Go 测试；当前已运行的旧后台进程不会热加载新二进制。

2026-10-06 重启新后台后的真实协议测试：新增的 Store `authenticate` 请求返回
HTTP 403，随后流程被安全截断，没有提交 `volumeAuthenticate`，也没有产生扣款。
这确认当前 Apple 账号要求 SAP 签名的 App Store 登录会话；仅凭 GSA 的
`icloud.storage.buy` 令牌仍不足以建立 `MZFinance` cookie。项目现在会把该阶段报告为
`purchase-store-authentication`，避免继续发送无效购买请求。

## 反汇编复核（2026-10-06）

`AKValidateAppleIDCredentials` 位于 `iCloudHome.exe+0x24f610`。官方静默
验证构造 `AKClientID=com.apple.iCloud`、`AKAuthenticationIsReauth=true`、
账户字段和 `contextID`，成功后把 `AKHeartbeatToken`、`AKIdentityToken`、
`AKAltDSID` 写入 AuthKit 本地安全存储。项目当前的 GSA 登录能取得对应的
`com.apple.gs.idms.hb` 与 `GsIdmsToken`，但尚未声称它等同于 Windows AuthKit
本地验证状态。

原生储存升级入口来自账户服务数据的 `mme-info.quota.quotaUpdateURL`。
AOSKit 会把 `$APPLE_ID$`、`$DS_PRS_ID$` 和字面量 `$UDID$`（值为 `123`）
替换后请求该地址；这条入口不是硬编码的 `presentOffers`。`StorageUIWnd`
注入的 `deviceUDID` 来自另一套 BIOS/硬件标识，页面购买表单里的 `guid`
实际与 `prsId` 绑定，和 SAP 的 MAC 硬件 GUID 无关。

项目已改为优先使用 `quotaUpdateURL`，保留服务数据下发的 URL 和页面返回的
Store offer；提交前会校验两个 Apple ID、`prsId`、Store token、iCloud token、
原始报价和 `okURL` 绑定，并按 HTML 控件顺序编码表单。SAP 签名只用于原生
Store plist 登录；`volumeAuthenticate` 是 WebView 的普通表单提交，不再把
SAP 签名误加到该页面请求。续期或重新验证时会清掉旧 Store cookie，避免跨会话复用。

进一步复核 AOSKit 后确认，原生 `quotaUpdateURL` 请求会由 AOSKit 统一加入
`X-Mme-Client-Info`、`Accept-Language`、`X-Mme-Timezone` 和请求头字典；这些头
不属于 WebView 的 `volumeAuthenticate` 表单。购买页面只保留 Store cookie、
`X-Apple-Store-Front` 和表单自身的隐藏字段，不能把 GSA 的
`com.apple.gs.icloud.storage.buy` 令牌继续附加到表单提交。

2026-10-07 的脱敏实测中，方案读取和购买表单校验均成功，表单 POST 仍返回空
HTTP 400；请求带有 14 个 Store cookie、Storefront 且收到新的 `Set-Cookie`，
没有重定向。项目保留该失败状态，不把它当作订阅成功，也没有检测到扣款。

随后按 WebView2 154.0.4258.53 的浏览器 UA 提交，`volumeAuthenticate` 返回
HTTP 302，目标为固定的 `https://volume.itunes.apple.com/`。该根地址公开 GET
本身返回 404，说明它是 Windows iCloud 的外部购买交接点，不是可由普通 HTTP
客户端完成的确认接口。项目现在把这个目标报告为 `handoff`，要求外部 Apple
支付客户端完成最终确认；不会把 302 或 404 写成订阅成功。

当前运行中的后台仍是旧进程（PID 8080）；新二进制已构建到
`.cache/go-build/trusted-device-new.exe` 和 `.cache/go-build/dashboard-new.exe`，
全量 `go test ./...` 通过。尚未重启在线后台，因此没有把未实测的订阅结果写成成功。

## 反汇编复核（2026-10-07，四路并行）

对安装目录 335 个 PE 的静态复核推翻了"无注入"的初始判断，官方购买链路的完整模型见
`.cache/re-synthesis.md`，分报告在 `.cache/re-storage-ui.md`、`.cache/re-aoskit.md`、
`.cache/re-webview-handlers.md`、`.cache/re-binary-sweep.md`。

- AOSKit 只产出 `{AOSRequestMethod, AOSRequestURL, AOSRequestHeaders}`（`quotaUpdateURL`
  模板替换 `$APPLE_ID$/$DS_PRS_ID$/$UDID$→"123"`，不转义）；**全目录不存在 native 购买代码**，
  订阅确认完全发生在网页里。
- `StorageUIWnd` 覆盖了 `EWV::EdgeWebView` 的请求槽（`0x1401f28c0`）；宿主导航时携带
  AOS 头集合（`X-Mme-Client-Info`、`Accept-Language`、`X-Mme-Timezone`、
  `X-Mme-Device-Id`、`X-Mme-Legacy-Device-Id`、`X-Mme-Local-User-UUID`、
  `Authorization: Basic base64(dsid:mmeAuthToken)`），实拍显示**页面自发跳转（含
  `volumeAuthenticate` 表单 POST）带浏览器头 + WebView2 UA，不带该集合**——两种请求
  形态见实测一节，代码按此分步复刻。
- 页面桥（`InternetAccount`）只有 close/cancel/getUserDSID/resizeTo/openWindow/openExternalLink/log；
  宿主不做任何 URL/标题判定，完成由网页调 `close()` 决定。`X-Apple-GS-Token` 在官方客户端
  是死常量，从未发送。
- 代码最终形态：**入口导航**带 AOS 身份头 + iCloud UA（无此则 Apple 返回无内容页面，
  解析不到报价）；**`volumeAuthenticate` 表单 POST** 用浏览器头 + `Edg/154.0.0.0`
  UA，不带 `X-Token/X-Dsid/X-Apple-Store-Front/ADSID/Authorization/X-Mme-*`
  （与官方实拍一致）。`storageInfoURL` 支持官方大写占位符；已购容量解析改为宽容
  （官方语义是 `quota_status.paid_quota` 布尔）。默认购买走"无 Store 会话"路径，
  原 SAP 登录变体保留为 `code=store-session`。

## 2026-10-07 官方客户端实测（决定性）

用 CDP + UI Automation 驱动官方 iCloud 15.8.118（WebView2 154），在真实 UI 上
走完了同一台机器上的 50GB / ¥6 购买：主界面「升级」/「管理储存空间」按钮 →
`p121-quota.icloud.com/quotastatic/desktopclient/v2/index.html`（储存 UI，宿主桥
`InternetAccount` 在此窗口可用）→ `presentOffers` POST 200 → 50GB `buyProduct`
GET 200 → 填入账户密码 → 提交 `volumeAuthenticate`。

实测结果：

- 官方提交的 `volumeAuthenticate` POST 请求头只有浏览器头 + WebView2 自带的
  Edge UA（`Edg/154.0.0.0`），**没有** `Authorization`、`X-Mme-*`、`X-Apple-*`
  注入头；表单字段集与项目代码生成的完全一致（`xToken` 为空、无额外 blinded
  字段，提交按钮即点号索引控件）。
- **Apple 对官方客户端的同一请求返回空 HTTP 400**（text/plain、空 body），
  窗口最终显示 Chromium "HTTP ERROR 400" 页面——与项目实现收到的失败完全相同。
- 主界面「升级」按钮直接打开的是**不带宿主桥**的另一窗口，页面 JS
  （`its.client.getUserDSID`）报错、Buy 按钮保持禁用；「管理储存空间」打开的
  才是带桥的储存窗口。该差异属于官方 UI 的窗口分工，与购买被拒无关。

结论：项目的购买实现与官方客户端行为等价；当前账号在苹果服务端被拒绝
（两端同样 400），属于账号侧前置条件问题，而非本地实现缺陷。可能的账号侧
原因：该 Apple ID 在 CN 商店从未完成过首次购买/商店账户开通
（页面携带 `wasWarnedAboutFirstTimeBuy=true`），需要先在完整商店端
（iPhone 的 App Store / 设置中的 iCloud 储存管理）完成一次购买/付款方式确认后，
Windows 端流程才可能通过。

### 项目侧的实测矩阵（2026-10-07 同会话）

| POST 形态 | Apple 响应 | 后续 |
|---|---|---|
| iCloud UA + AOS 身份头 | 空 HTTP 400 | 终止 |
| 浏览器头 + `Edg/154.0.0.0`（官方实拍形态），无 Store 会话（全新 jar） | HTTP 302 | `Location: https://volume.itunes.apple.com/` → 带 cookie GET 仍 404 |
| 同上，但先做 SAP 原生 Store 登录（`code=store-session`） | HTTP 302 | 同上 |
| 官方客户端（真实 UI、真实密码） | 空 HTTP 400 | 窗口显示 HTTP ERROR 400 |

- 所有形态都未产生扣款；事后配额查询仍为 5 GB 免费 / 0 付费
  （`storage-info`：total 5.00 GB、paid 0.00 GB）。
- 入口导航必须携带 AOS 身份头（`Authorization: Basic base64(dsid:mmeAuthToken)` +
  `X-Mme-Client-Info`），否则 Apple 返回无内容页面，解析不到任何报价链接；
  这与"宿主导航带注入头、页面自发请求不带"的实拍一致。
- 两种 302 证明 `volumeAuthenticate` 的"浏览器形态"请求被边缘放行，但苹果
  随即把流程导向 Volume Store 根（对任何直连都 404），并未建立购买会话；
  官方形态（同样的浏览器头）反而被 400。两条路都未走到 `okURL`/成功页。
- 复现工具与脚本：`.cache/cdp-{inspect,capture,drive,buy}.py`、
  `.cache/rtti_walk.py`、`.cache/vtable_scan.py`、`.cache/dump_at.py`。
  UI Automation 提示：主界面按钮 `AutomationId=ManageStorage`（带宿主桥的储存
  窗口）与 `UpgradeStorage`（无桥的小窗口）行为不同。

## 2026-10-07 官方客户端验证实测（当日会话）

用 UI Automation + CDP 驱动官方客户端走完验证：点「验证 Apple 账户」→ 2FA 验证码窗口
（6 个输入框，`digit0..digit5`）→ 验证码**由本项目自己的可信设备通道收到**
（dashboard `code` 动作返回，如 995648），填入后进入「正在登录…」。

结果与证据：

- 第一次：填码后 → `AKValidateAppleIDCredentials returned -9002` → 弹窗
  「验证失败：连接 Apple 账户服务器时出错。」（本地化 DLL 字符串表中该文案属于
  AuthKitWin 的"服务端/连接失败"一档）。
- 第二次（重新发码 + 输入密码对话框）：填码后再次失败，弹窗「验证失败，请重试。」。
- 抓包（dumpcap，WLAN 接口）显示：全程 `gsa.apple.com`(17.23.96.10)、
  `setup.icloud.com` 的 TLS 握手完整、双向数据正常，**无服务端 RST、无 TLS 告警、
  无重传异常**（仅客户端自身关闭旧连接时的 close+RST）。即失败在**应用层**，
  不是网络传输问题。
- 官方二进制的 TLS 栈不响应 `SSLKEYLOGFILE`，无法解密看 HTTP 状态。

**新发现：现代配额服务（iOS 路径）对我们的凭据完全可用**

`iCloudWinPref.plist` 缓存了账号自己的入口（AOSKit 下发）：
`https://p121-quota.icloud.com/quotaservice/external/ios/<dsid>/<token>/getManageCloudStorage`。
用本项目会话凭据 + **anisette 头**（必需；无 anisette 时 401）GET 该地址返回
`200 application/vnd.apple.xmlui`：

```xml
<xmlui action="push">
  <navigationBar title="Manage Storage" backButtonTitle="Back"></navigationBar>
  <tableView><section>
    <buttonRow label="Change Storage Plan" align="left" name="buyStorage"/>
  </section></tableView>
</xmlui>
```

即 iOS/现代路径的"更改储存方案"入口命中；`store-session`（SAP 商店会话）对该
地址是 401，说明它认的是 **iCloud 会话 + anisette**（与商店会话无关）。
后续探测（action=buyStorage 等参数变体）已加入 `icloud/probe.go`，待账号冷却后执行。

**账号侧风控迹象（重要）**

- 当天高频登录后，worker 登录出现 `gsa: complete rejected (protocol=s2k, ec=-22406)`，
  此前亦有 HTTP 429。Apple 文档建议不要连续重试（会延长保护）。
- 官方客户端在验证前的静默校验失败码（-7006/-7018/-9002）自 10-05 安装起即存在，
  与"重复登录触发保护"的时间线一致。
- 结论倾向：**官方客户端的 2FA 后失败与账号被临时风控有关**，需冷静期后单次重试；
  若仍失败，再走账号侧解锁（iforgot.apple.com/unlock 或等待保护解除）。

### 现代配额服务的实测补充（同日）

- `POST getManageCloudStorage`（同 URL，body 任意）→ 200 JSON **配额/商务状态**：
  `accountStatus:"ACTIVE"`、`status:"200"`、`defaultStorageInBytes:5368709120`、
  `currentQuotaPlanInBytes:0`、`totalPurchasedQuotaInBytes:0`、`purchaserDsid:0`、
  `QuotaThresholdWarning:"NONE"`，含 `usageByApp`（cloudkit/mail/mobilebackup）。
  → **账号在配额服务视角是 ACTIVE**（无锁定/无欠费迹象）。
- `action=getStoragePlans / storagePlans / getStoragePlanOptions / changeStoragePlan /
  getStoragePlanTiers`、以及带 50GB 参数（offerName/salableAdamId/storagePlanInBytes）
  的 `action=buyStorage` 变体：**全部返回同一份状态 JSON**（参数被忽略）。
- 结论：XMLUI 里 `<buttonRow name="buyStorage">` 是**客户端本地动作**（iOS 侧弹本机购买
  UI/转商店），不是服务端购买端点；真正的成交仍走商店侧（MZFinance / StoreKit）。
- 因此"未验证设备"仍是最可能的商店拒绝原因；客户端验证的最后一跳（2FA 后的
  交换）失败 + 苹果限流是当前两个障碍。

### 工具改动（本轮）

- `icloud/probe.go` 新增 `ProbeSpec`/`ProbeCustomEndpoints`；worker 的 `storage-probe`
  现在优先读取 `<state>/probe-urls.json`（本地诊断用，HTTPS-only，最多 40 条），
  **改探测清单不再需要重启 worker**（每次重启=一次登录=消耗苹果限流额度）。
- 探测输出做了脱敏：长路径段/长 token/邮箱/长数字一律 `<redacted>`，只保留
  主机名、短路径段、参数名与短状态字符串。

## 2026-10-07 决定性结论：购买失败的根因是账号无付款方式

用 Edge(CDP) 在网页端完成了完整登录（idmsa 表单 + 2FA 验证码，码仍来自本项目
可信设备通道），**网页登录成功**（进入 account.apple.com/account/manage）。
随后读取各分区：

- **付款与配送 → 媒体购买项目和订阅项目：`无付款方式`**
  （原文："你没有为媒体购买项目和订阅项目设置任何付款方式。"），配送地址同样未设置。
- 订阅：加载正常（无有效订阅）。
- 设备：`appleservices / MacBook Pro 15″`（序列号 `AS917DFEBEDD`，即本项目注册的
  可信设备，账号**唯一**的受信任设备，承担验证码接收）、`MacBookPro / MacBook Pro 13″`、
  `WANGSHEN233 / Windows`。

**结论：iCloud+ 是订阅制、走媒体购买计费；账号没有任何付款方式时苹果会拒绝
购买——这与"官方客户端与本项目实现都收到空 HTTP 400、促销接口返回通用错误"
完全一致。** 购买测试的下一步必须由账号持有人添加付款方式。

### 客户端验证失败的排除项（同日）

- 网页登录成功 ⇒ 账号本身正常；Windows 客户端失败是**设备/客户端侧**问题。
- 客户端首次失败在安装后 17 分钟（10-05 12:51），早于本项目任何设备注册
  （19:30）⇒ **与本项目注册的"MacBook Pro 15″"设备无关**（不因此移除它；
  移除还会切断验证码接收通道）。
- 早期 ADI 实验（`native-adi-probe.py`、`clearadi-stub`）均为用户态直接调用
  CoreADI 导出函数，不修改系统状态。
- 结论：客户端（AuthKitWin + CoreADI anisette 路径）在本机的登录交换持续被
  拒，原因未定位到可操作项；但购买路径已不需要它（见上）。

## 2026-10-07 深夜：商店链路逆向的决定性修正

购买始终失败的原因已定位并修复（多项），全部有实测对照：

1. **认证端点换代（26HOTFIX24，2026-06）**：bag 广告 `https://auth.itunes.apple.com/auth/v1/native/fast`；
   旧的 `/WebObjects/MZFinance.woa/wa/authenticate` 只回 301/HTML，其 token 会被购买流程以
   `failureType 2034` 拒绝。代码现在**强制规范化到 native 端点**（无尾斜杠会 301）。
2. **设备 GUID 必须是物理网卡 MAC**：本机的 VMware/WSL/**TAP-Windows(00:FF:…)** 虚拟网卡
   MAC 均低于真实 WLAN 网卡，按字节序会被优先选中；**苹果对这类身份静默丢弃（HTTP 204）**。
   现在优先选择"在线且非虚拟"的网卡（`storeHardwareIdentity`），并跳过 00:FF/00:15:5D/00:50:56 等前缀。
   实测：换成真实 MAC 后同一请求从 204 变 **200 + 要求 2FA 验证码**。
3. **商店会话已能建立**：`Apple Store 认证已通过，购买会话已保留`；
   验证码可直接取自本项目的可信设备通道（dashboard `code`）。
4. **购买提交需要 plist POST**（不是表单）：按 ipsw（`blacktop/ipsw`，经 jsdelivr 获取）的实现，
   购买应 POST XML plist 到 **`https://p<pod>-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/buyProduct?guid=<硬件GUID>`**，
   键名 `salableAdamId`(int)/`appExtVrsId`/`guid`/`pricingParameters`/`productType`/`origPage` 等，
   头 `X-Dsid`/`iCloud-DSID`/`X-Token`/`X-Apple-Store-Front`。pod 取自登录响应头。
5. **限流**：商店认证端点对高频请求回 204/301/403/404/503 混合；放行是概率性的
   （实测约 5~12 次里放行一次）。可用 `APPLESERVICES_STORE_PROXY=host:port`（SOCKS5）
   让商店链路走不同出口 IP；该变量只影响商店/购买流量。

**注意**：官方 Windows 客户端的验证失败（-9002）很可能与第 2 条同源——它同样在枚举网卡时
可能选中虚拟 MAC；禁用 TAP/VMware/WSL 虚拟网卡后重试其"验证 Apple 账户"是下一步验证方向。

## iCloud+ purchase: the terms wall and the account block (2026-10-07)

Two distinct blocks sit between a correct `buyProduct` request and a completed
iCloud+ subscription. Both were reproduced and identified.

### 1. Media services terms (failureType 3038) — solved

`buyProduct` answers `3038` / `MZCommerce.TermsNotAccepted` when the account has
not accepted the current media services terms. `termsCheckSrv` reports the state
and the pending version:

    GET https://p<pod>-buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/termsCheckSrv?guid=<MAC>
    -> {"isCurrent":false,"latestTermsVersionId":"290399", ...}

The accept action is advertised in the `termsPage` document's own form, which is
only served to an **AppStore fingerprint**: with the Configurator or iTunes
user agent the same URL returns a 1-byte body.

    GET .../com.apple.jingle.app.finance.DirectAction/termsPage?xToken=... 
        User-Agent: AppStore/3.0 (Macintosh; OS X 15.4.1; 24E263)
    -> 60785-byte document containing:
       <form method="post"
             action="https://buy.itunes.apple.com/WebObjects/MZFinance.woa/wa/agreeToTermsUpdate?termsId=290399">
         <input id="iagree"  name="2.0.1.1.3.0.5.1.1.7.3.9"  value="2.0.1.1.3.0.5.1.1.7.3.9" type="checkbox">
         <input id="continue" name="2.0.1.1.3.0.5.1.1.7.3.17" value="Agree" type="submit">

The opaque numeric field names are per-render and must be replayed verbatim.
The submission must run on **one persistent cookie jar** spanning the terms page
and the agreement post: a fresh jar per request is answered
`2002 MZCommerce.BadPasswordToken ("Your password has changed")`. With the jar
kept, the same request returns a `Congratulations / You've accepted the new
Terms of Service` page and the 3038 block is gone.

### 2. Account not iCloud-capable (failureType 3064) — external block

With the terms accepted, `buyProduct` returns the real confirmation dialog:

    dialogId   SRVOrderManager.ConfirmBuyingAutoRenewingSubscription
    message    iCloud+ with 50 GB of storage
    explanation Plan automatically renews at ¥6.00/month ...
    okButtonAction.kind       Buy
    okButtonAction.buyParams  cc=CN&salableAdamId=6449562876&...&hasConfirmAutoRenew=true
                              &nextChargeDate=7 Nov 2026

Confirming it (resending with `hasConfirmAutoRenew=true` from that buyParams)
is answered:

    failureType 3064
    dialogId    MZCommerce.StoragePurchaseOnNonStorageCapableAccount
    explanation Only Apple Accounts used for iCloud can purchase an iCloud+ plan.

So the purchase path itself is correct and complete; Apple refuses because it
does not treat the account as iCloud-enabled. The iCloud delegate states why:

    POST https://setup.icloud.com/setup/iosbuddy/loginDelegates
    -> status-message: "A server problem is blocking Apple ID sign in. Try signing in later."

A further sign-in attempt immediately returns `gsa: complete HTTP status 429`.
The account is rate-limited / under review on Apple's side, not rejected on
protocol grounds. Until one real iCloud login succeeds (delegates return
service-data with an mmeAuthToken), no iCloud+ purchase can complete regardless
of request shape.

### Parameters that matter

* `offerName=com.apple.icloud50GB_monthly`, `storagePlanInBytes=53687091200`
  and `cc=CN` are **required**. Without them `buyProduct` answers
  `MZCommerce.SystemError` ("Unable to process your request"), which reads as a
  transient fault but is deterministic.
* `presentOffers` returns `5093 "Cannot load storage plan"` for a
  non-official client, so the page-driven route cannot be reproduced; the
  native `buyProduct` plist route does reach the business layer.
* `https://buy.itunes.apple.com/commerce/account/subscriptions/icloud/update`
  (the modern subscription API implied by the store bag) is **404**; only
  `speedysub .../manage/list` answers, and correctly reports `active: []`.
