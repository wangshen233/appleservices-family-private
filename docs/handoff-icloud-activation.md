# iCloud 激活交接：复核后的事实与待验证项

**复核日期**：2026-10-08
**目标**：让用户指定账号通过 iCloud 登录，取得 `mmeAuthToken`，并用该令牌读到配额。
**状态**：`02-account` 和最新目标 `03-account` 的独立 Mac v2/NAC 路径均已通过 MME 与配额验收。`01-account`、`01-primary` 仍未完成 2FA。各账号使用独立诊断 Mac 和 APS，不转用其他账号的成功证据。`03-account` 后续新 GSA 登录的 delegate 返回 3001，但首次接受的 MME 会话仍能实时读取配额及套餐；不能将首次成功概括为所有新登录或 worker 都已修复。

### 最新目标 03-account：设备验证、条款、激活与套餐

- 手机不可用后停止短信路径，改为向账号现有受信任设备申请验证码。用户提供设备码，Apple 接受，完成本账号新 GSA 认证；验证码用后删除。
- 使用本账号独立 UUID、序列号、网络地址、ADI 与真实 Mac APS 证书/令牌。缺失板级字段采用明确标记的合成诊断值；没有使用另一个账号或实体 Mac 的身份。
- Apple 返回 CN 条款（iCloud 634301、媒体服务 290399）。取得本账号用户明确授权后提交一次，返回 HTTP 200、`success=true` 及接受条款响应头。
- 条款后的 delegate 与配额成功；另用本账号 517 字节 NAC 验证 Mac v2 delegate，同样取得非空 MME，读到总容量 5 GiB、已用 0、付费容量 0。accepted checkpoint 仅在配额成功后写入私有目录。
- 用同一模拟 Mac 新登录后，GET 查询当前设备详情，`trustedDevice=true`，序列号匹配，`pushDevice=false`；设备摘要为 `supportsVerificationCodes=false`。信任已验证，不能因此推断该 Mac 可生成有效设备验证码或已实测接收验证推送。
- 旧 GSA checkpoint 的设备查询及稍后的 v2 重用返回 401；新 GSA 登录后的 v2 返回 `HTTP 409 / ICLOUD_UNSUPPORTED_DEVICE / 3001`。保留这些失败，不自动重提条款，不据此否定首次已通过的令牌与配额验收，也不宣称新的 delegate 路径稳定。
- 随后直接复用首次 accepted MME checkpoint，实时配额再次成功。GET-only 套餐查询返回可用 CN 月付方案：50 GB / CNY 600 分、200 GB / 2100 分、2 TB / 6800 分、6 TB / 19800 分、12 TB / 39800 分。没有提交购买、支付或购买表单；套餐可用不证明实际付款可成功。
- 新 CLI 模式：`-trusted-device-code -sms-code-file <新私有文件>` 申请并验证设备码；它与短信/本地码模式互斥。`-icloud-resume <accepted checkpoint> [-storage-plan]` 使用已接受的本账号 MME，只读配额及可选套餐，不再做 GSA、delegate、条款或购买。

### 03-account 用户另行授权购买后的进展

- 用户明确授权购买 CN 50 GB、CNY 600 分/月。`tools/icloudpurchase` 复用本账号 accepted MME，先确认免费容量与精确报价，再创建拒绝覆盖的私有 attempt journal。发现已有付费容量时不发送新购买。
- 首次 Windows 页面购买流程的后续订阅请求返回 HTTP 404；实时容量仍为 5 GiB、付费容量 0。没有完成标记，不能据此确认订阅或是否扣款。
- 原生商店认证改为本账号诊断 Mac 的独立网络身份，不使用所有账号共享的 Windows 物理网卡 GUID。初次返回需要 2FA；用户提供的新设备码仍未取得商店会话。这个应用层拒绝不自动重复验证。
- 用户要求使用已添加的 Mac 后，直接由本账号 native ADI 在内存生成验证码，没有申请短信、借用其他账号设备或打印验证码。商店端依次遇到 503、404、无有效跳转地址的 301；一次最多三次认证路由重试后仍以 204 结束，没有有效商店会话。因此尚无本地码被商店接受或原生购买成功的证据。
- 原生购买代码已修正顶层 plist（去掉无来源的 Header/Request 包装），保留 Apple 报价中的 offerName、storagePlanInBytes、cc 和精确价格；只在用户授权后消费 Apple 返回的特定自动续费 Buy 确认，复核报价和会话绑定。未知 dialog、不匹配报价、限流或不明购买响应都不自动重提。
- 测试覆盖原生请求报价、修改价格/容量/账号后的拒绝、验证码拒绝与 429 不重试。协议测试通过不替代真实付费验收。当前购买仍未完成；后续应先解决商店认证，且继续复查实时容量，禁止盲目重放旧订单。
- 继续使用本账号 Mac 生成验证码并按 Apple 当前 App Store bag 保留 native endpoint、SAP 签名和 App Store 请求头后，商店认证仍返回 HTTP 429（响应给出 Retry-After=3；等待后再次返回 429）。未发送购买 POST，实时容量仍为 5 GiB、付费容量 0。Apple 的 301/302/404/503/429 仅是认证链状态，不能当作成功或扣款。

### 改按项目购买 skill 执行后的复核

- 项目已有 `.claude/skills/icloud-plus-purchase/SKILL.md`，与 Mac/NAC 激活 skill 分属两个流程。此前只读取激活 skill，并错误概括“没有购买成功记录”；该结论已撤回。
- 旧 Hotmail 的成功证据在原 Claude 项目会话中：Apple 返回 `purchaseSuccess` 与 CNY 购买指标；后续 `speedysub` 订阅详情报告实际 `50 GB` 配额，并有关闭自动续费后的状态记录。不是当前 QQ 账号的验收证据，也不能当作 Hotmail 当前实时状态。
- `icloudpurchase -claude-skill -native-store` 现使用本机物理网卡商店 GUID（与旧成功记录一致），不改 QQ 原来的 Mac/ADI/APS 身份。采用成功登录代码的完整 Configurator UA、保留默认 HTTP 传输能力并禁用代理。认证先只提交密码，Apple 要求 2FA 后才在同一 cookie jar 中加入验证码。
- 该分支直接发送 skill 的原生 plist `buyProduct`，含整数 storagePlanInBytes、appAdamId 和完整参数；仅在已授权且报价/会话匹配时回放 Apple 的完整 Buy 参数及额外续费确认字段。3038 时只接受用户此前批准的媒体服务版本，条款页与提交共用 cookie jar。
- 下单前查询已有 iCloud 订阅以避免重复购买；下单后通过 `speedysub` list/detail 的 specialSubInfo、付费价格核验真实 50 GB。支持新私有商店 checkpoint 保存与账户/物理 GUID 绑定恢复；不保存密码或验证码。
- 本次做了有上限的直连认证对照，含 Go 传输、Python requests 传输和完整成功请求设置。Apple 仍仅返回 204、无有效跳转的 301/302、HTML 403/404/500/503，没有商店令牌。当前没有进入原生下单或确认请求，QQ 的实时 MME 配额仍为 5 GiB、付费容量 0。以上不能证明账号不支持购买，也不能报告订阅完成。

### 当前目标 01-account 的最新直连复测

- Windows CoreADI 使用该目录下独立且稳定的 Windows 身份，GSA 使用新会话。用户提供的两个密码变体已分别测试；其中一个通过服务器 SRP proof 并进入 2FA，另一个在 `complete / s2k` 返回 `ec=-22406`。文档不保存密码。
- 已通过的变体仍未完成双重验证：`/auth`、显式 phone ID 1 的短信发送接口及可信设备验证码接口都返回 HTTP 403。phone ID 1 只是明确选择的诊断候选，没有证据确认它是本账号的有效号码标识；没有轮询其他 ID，也没有短信发送成功证据。
- 浏览器 UA/Fetch 头对照和仅更换 2FA 连接复用方式的对照也未解除 403。所有对照保留该 Windows 身份，清除项目和环境代理，保持 Apple 直连。
- 该账号现有 Mac native profile 的新 GSA 登录在 `init` 返回 `ec=-29004`，尚未进入密码校验。不能用它推断密码错误。
- 当前没有新的完整 GSA/PET、没有新接受条款的证据、没有通过 MME 与配额验收，也没有提交任何购买请求。403 和上述错误码都不能单独证明账号冷却或唯一根因。

### 按技能复核后的最新进展（01-account）

- 保留原账号目录，在仅当前 Windows 用户可访问的隔离目录复制本账号 Mac 硬件配置，仅重新注册该设备的 ADI。设备 UUID、序列号和 APS token 沿用本账号；没有复制 `02-account` 的设备或凭据。
- 使用 `accountsetupcheck -native-profile <隔离目录>` 后，新注册数据的 GSA init/complete、SRP 服务器 proof 和 SPD 解密通过，进入 `secondaryAuth`。原配置仍保留；这次成功不能被报告为 worker 已修复。
- 在同一隔离硬件配置上完成证书、NACInit、Apple session-info、NACKeyEstablishment 和 NACSign，输出 517 字节 validation data。ROM/MLB 仍是明确标记的合成诊断字段。
- 读取短信配置时 Apple 返回 HTTP 423 和非空 JSON：`serviceErrors[0].code=-22981`、`securityCode.tooManyCodesSent=true`、`noTrustedDevices=true`。另外 `tooManyCodesValidated=false`、`securityCodeLocked=false`、`securityCodeCooldown=false`。因此本次有明确的短信发送次数限制证据，不能把它说成账号整体锁定，也不能推算恢复时间。
- 已停止继续申请短信。本地生成验证码的提交曾返回 `-21669`，不能代替该账号必需的短信验证。当前没有本次短信成功发送的证据。
- `APPLESERVICES_2FA_PROFILE=authkit` 的完整 AppleIDSettings 请求上下文对照仍返回同一 423。工具现保留脱敏错误码与限制标志，原始响应仅写入显式指定的私有目录，不打印消息、手机号、验证码或令牌。
- 下一步是用这个隔离身份完成短信验证并取得有效 PET，再核验本账号条款状态，提交对应 NAC 的 v2 delegate，最后读配额。此时尚未提交新的 v2 激活请求；不应跳过 2FA 或添加未经本账号条款成功确认的 `termsAccepted=true`。
- 独立工具的 accepted checkpoint 已调整为配额查询成功后才写入，且拒绝覆盖已有私有输出。

### 用户另行指定的 01-primary

- 只处理用户确认的 `01-primary`，未改变其他账号。原配置的设备、序列号、Mac provisioning 与 APS token 绑定检查通过；旧 PET 的只读 setup 请求返回 HTTP 401，未将该会话视为当前有效登录。
- 原 Mac ADI 的新 GSA 登录在 init 返回 `-29004`。保留原配置，建立本账号同一设备身份的隔离副本并重新注册 ADI 后，密码验证和服务器 proof 通过，进入 `secondaryAuth`。
- 该账号自己的 Mac 配置已完成 NAC 三个调用和 Apple session-info 交换，validation data 为 517 字节；缺失 ROM/MLB 的合成值仅用于明确标注的诊断。
- 双重验证配置响应为 HTTP 423，Apple 明确返回 `ec=-22981`、`tooManyCodesSent=true`、`noTrustedDevices=true`。没有本次成功发送短信的证据，未继续申请短信；没有完整新 PET、条款提交、v2 delegate 或配额成功的证据，未购买。
- 与 `01-account` 收到相同限制标志，不足以区分账号级与出口级限制，也没有服务器给出的恢复时间。全程清除代理并保持直连。

### 用户要求逐账号使用独立 Mac 后的修正

- 核对确认：UUID、序列号、磁盘/平台 UUID、ADI MID 各自独立，但旧 helper 的网卡地址为固定值，缺失 ROM 时 NAC 又沿用此固定地址；旧 worker 的 Mac ADI 模式还自动使用同一个 Windows APS 守护进程令牌。之前仅核对 UUID/序列号不能证明完整隔离。
- 新建诊断 Mac 的网卡地址现由账号设备 identifier 稳定派生。两个待激活账号的 MAC、ROM/MLB 及对应加密值已在私有备份后分别修正，并重新注册各自 ADI。保留成功账号配置，不为改变型号而轮换已接受身份。
- 通过 pinned 原生模块分别向 Apple 注册 Mac APS 证书及真实 32 字节令牌。两个账号各自重连后令牌不变；Go courier 客户端直连成功，证书/私钥和硬件令牌绑定一致。新增 `tools/mac-push-check.py` 和 `tools/macpushcheck`，输出仅含状态和长度。
- `nativeadi/helper.py` 现保留 Mac APS keypair；`-native-adi` 不再隐式强制 Windows 共享 APS。原生 Mac worker 默认读取该账号 `push.json`。Windows daemon APS 仍可通过显式 `-native-push` 选择为独立诊断路径。
- 已停止旧 `01-primary` 项目 worker 后换入修正配置。两个待激活账号的 native hardware/state、独立 `push.json` 及匹配的 517 字节 NAC 均已安装在各自目录；原配置保留在仅当前用户可访问的私有备份中。
- 换入后重新检查，三个账号的 UUID、序列号、MAC、有效 ROM/MLB、ADI MID、APS token 均不同；两个新 Mac 的证书/keypair 在后续 GSA 登录中仍被保留。成功账号文件及 accepted checkpoint 的哈希未改变。
- 各使用新配置复核一次 GSA：两个账号的密码和服务器 proof 均通过，短信配置仍返回 HTTP 423、`-22981`、`tooManyCodesSent=true`、`noTrustedDevices=true`。因此隔离修正不能被报告为短信限制解除或 iCloud 激活成功；未继续请求发送短信，未购买。
- `nativeadi`、`aps`、`cmd/trusted-device` 回归测试通过。独立 Mac APS 与 NAC 的实测不替代 MME/配额验收。

### 02-account 的账号设备信任核验

- 用户要求核验本地模拟 Mac 是否受信任。旧 GSA checkpoint 查询设备列表返回 HTTP 401；随后用相同账号和原 Mac 身份新登录，只执行设备 GET 查询，没有登记设备、请求短信或同意登录通知。
- Apple 返回 10 条设备摘要，其中本次 Mac 为 `currentDevice=true`、`supportsVerificationCodes=true`。摘要不含序列号和显式 trust 字段，不能据此把缺失字段解释成不受信任。
- 沿 Apple 返回且校验过的当前设备 GET 详情链接查询，序列号与本地配置匹配，返回 `trustedDevice=true`、`untrusted=false`、`supportsVerificationCodes=true`，确认该模拟 Mac 已在账号内并被标记为受信任。
- 同一详情的 `pushDevice=false`。受信任和支持验证码的标志不能替代当前本地客户端接收推送验证码的实测。全部原始设备数据仅保存到私有文件，文档不保存设备 ID、序列号或令牌。

### 02-account 本地验证码与推送的实际测试

- 新增 `tools/trustedcodecheck`，用本账号原 Mac 作为接收端，以独立 UUID、序列号、ADI 和 Mac APS 的临时诊断身份作为发起端。接收端的推送令牌与本地已保存硬件匹配，新登录无需 2FA；不调用设备登记、条款、购买或通知批准接口。
- 第一项测试：临时发起端通过密码校验并明确需要 `trustedDeviceSecondaryAuth`。Apple 接受一次可信设备验证码请求；本地 Mac 原生生成的六位码在内存中提交，Apple 接受，随后同一临时身份完成 2FA 并取得实际 PET。`local-code-proved-by-complete-2fa=true`，验证码未打印或保存。
- 第一项的 45 秒观察收到同账号 `cmd=1200` 状态通知。为排除通知队列在登录后被清空、立即验证码验证结束请求的影响，另做一次只观察推送的测试：保留登录过程中产生的通知，并暂不提交验证码。
- 第二项测试：独立临时发起端同样需要可信设备 2FA，请求被 Apple 接受。在 45 秒窗口内，本地客户端收到同账号 `cmd=100` 登录验证请求，未收到无法归属的未知通知；`approvals-sent=0`。因此确认本地可接收该账号 AuthKit 登录验证请求，不依赖 `pushDevice` 字段推断实际投递。
- AuthKit 登录通知与六位验证码是两项证据：此次收到的是登录请求通知，六位码由本地原生模块生成，并已通过真实 2FA 验收。没有声称通知本身携带明文验证码。
- 新 Mac 首次初始化时，原生模块会写出尚无 APS token 的硬件文件，其读取器不能恢复该状态。引导工具现保留刚创建的配置/身份句柄直到真实 Mac APS 激活完成，不填入假 token；已存在的完整 Mac 凭据仍保持重用。

独立 `accountsetupcheck -delegate-only` 提交流程已成功。严格 worker 的复测仍返回 `HTTP 409 / ICLOUD_UNSUPPORTED_DEVICE / reason-code 3001`；这表示 worker 的默认会话或请求上下文仍与已验证的独立探针不同，不能覆盖独立探针已经取得的 MME 和配额证据。

原文的“唯一根因已经定位”“代码侧彻底排除”“必须先登录真机”均超出了现有证据。原文快照保留在本地 `.cache/handoff-icloud-activation-original.md`，其中账号资料不应发布。

## 1. 原文中需要纠正的结论

| 原结论 | 本轮复核 |
| --- | --- |
| `X-Mme-Nas-Qualify` 是唯一根因 | 是 Mac/v2 路径的重要候选，尚无同一账号、同一设备、有效 PET、正确请求格式下的有/无 NAS 成功对照。错误名称不能单独证明某个头是唯一原因。 |
| 请求格式已经排除 | 错。Rust 的 `V2AuthRequest` 有 `serde(rename_all = "camelCase")`，实际键是 `protocolVersion`、`userInfo`；项目及旧 v2 探针发送了 `protocol_version`、`user_info`。内层 `client-id` 仍是 kebab-case。 |
| prefpane 给出的错误就是 iosbuddy 失败的真实原因 | 尚未证明。旧实现换 URL 后直接复用 iosbuddy body；HTTP 400 和响应中的 `protocolVersion=2` 不等于已经掌握该端点的完整请求协议。 |
| NAS 没有开源实现 | 错。公开的 pypush 有 NAC 仿真调用；另有公开 OpenAbsinthe 的 `new` / `key_establishment` / `sign` 实现。它们依赖苹果二进制和硬件数据，不等于从零实现的纯签名算法。只检查一个 Stub 仓库不足以得出不存在实现的结论。 |
| 占位符 bug 已经全部修好 | 错。旧 commit 只增加了可手动保存 blob 的工具；复核时 `internal/dashboard/identity.go` 仍生成 `native-adi`，`State.Provisioned()` 仍把它视为注册完成。 |
| 所有生成身份都从未注册 | 错。native ADI 独立使用 `native-adi/anisette_test/state.plist`，不依赖 `Device.ProvisioningBlob`。本轮该 native profile 已有 Mac provisioning、32 字节 client secret、60 字节 MID、400 字节 metadata。远程占位符问题不能推导出 native 路径也未注册。 |
| 本地/远程 anisette 完全没有差异 | 本轮相反：同一用户账号下，本地 native 在 GSA init 返回 `-29004`；远程路径通过 SRP 密码认证，随后在 `/auth` 收到 423。 |
| 出口 IP 已经排除 | 旧 delegate 没有使用 GSA 的 `AppleRootTransport`，因此没有应用 `APPLESERVICES_SOCKS_PROXY`；其代理实验并非整条链路使用同一出口。 |
| PET 每次调用一定消耗一次 | 原文未提供足以证明通用一次性语义的证据。应避免重用旧令牌推断新登录结果，每个有效对照取得新 PET，并记录令牌来源/时间而不打印值。 |
| 在真机上登录一次后 Windows 就一定能用 | 未验证。账号服务状态、设备身份、anisette、NAS 和会话有效期是不同条件。不能保证一台设备上的登录会让另一身份被接受。 |
| 读到 MME token 就能保证购买 | 错。令牌和配额读取只验证 iCloud 服务可用；商店会话、条款、支付及购买响应还需分别验证。 |
| 423 一定是短信限流；HTTPS 不可达一定是被墙 | 都是未经证实的归因。先保留 HTTP/传输证据，不能把本机观测升级成服务永久故障或确定网络政策。 |

## 2. 本轮真实服务器证据

首轮操作用户指定的 `state/accounts/01-account`；随后用户又提供一个账号，独立添加为 `state/accounts/02-account`。没有对已售出/归档账号发请求。

1. **旧 PET 的单次 legacy-v2 探测**：沿用该账号已有 native profile 和已保存 APS token，收到 HTTP 400、`X-Apple-Reason-Code: 3005`。这是旧会话结果，不能说明修正格式后的新登录结果。
2. **新密码、本地 native、原 worker 配置**：GSA `init` 返回 `ec=-29004` / environment mismatch。尚未进行密码校验、2FA 或 delegate。
3. **本地 native、普通 akd 配置**：仍在 `init` 返回相同错误。
4. **本地 native、普通 akd、去掉补加 MDLU**：仍在 `init` 返回相同错误。没有扩大到换账号或不断生成新身份。
5. **远程 anisette、普通 akd**：原 device marker 被识别为未 provisioned；实际 provision 后保存 **614 字节** blob。GSA init/complete、服务器 SRP proof 和 SPD 解密通过，进入需要 2FA 的分支；获取 `/auth` 验证信息时返回 **HTTP 423**。没有拿到验证码请求成功证据。

6. **远程 anisette、普通 akd、参考浏览器 UA/Fetch 头**：GSA 继续通过，获取 `/auth` 仍为 HTTP 423；该改动没有消除当前拒绝，未进一步循环申请验证码。

### 用户随后提供的第二个账号

- 新账号在本地 native 路径通过 GSA 密码认证、短信验证码请求和验证码验证，并通过 GSA 设备登记。
- 面板随后显示 online，但只读配额检查返回 authentication / HTTP 0；没有有效 MME 认证信息。
- 查明 `APPLESERVICES_ALLOW_DEGRADED_ICLOUD=1` 分支会在 delegate 失败时构造缺少 DSID/MME token 的 Client，并将其作为成功返回。已移除此虚假成功分支；回归测试覆盖旧启动器仍设置该变量的情形。
- 对同一账号运行严格新登录，无需再次 2FA；iosbuddy 返回顶层 status=0、mobileme status=1、没有 service-data。当前仍未完成 iCloud 激活。
- 新增 accountsetupcheck：以新取得的 GSA/PET/family token 读取 `/setup/update_account_ui`。HTTP 200，页面绑定当前账号，updateMode=true、deviceQualified=true、nasQualify 为空、条款页 countryCode=JP、tos-version=634501、ams-tos-version=290462。
- Apple 的页面脚本明确使用 `/setup/web/basic_auth_update_account/{account}/{udid}` 提交已有账号的条款更新，NAS 头只在页面给出非空值时发送。此接口尚未提交；待用户明确同意两项服务协议。请求计划见 `docs/icloud-setup-terms-plan.md`。

这些观测已足以否定“当前代码和客户端环境完全排除”，尚不足以认定 delegate 根因。

## 3. 本轮已落实的代码修正

- 新建账号不再写假的 `ProvisioningBlob`。远程 provider 把旧 `native-adi` marker 识别为未注册，执行真实 provisioning。
- native worker 接受 16 字节身份和空远程 blob；外部 code-provider 路径仍要求可用远程 provisioning 数据。
- v2 请求改为参考实现实际序列化出的 camelCase 外层键。
- 增加明确选择的 `legacy-v2`：向 signin/v2 URL 发送 legacy 请求体，作为 headless 兼容路径的实验入口；没有自动切换端点或重试 PET。
- delegate 使用 GSA 同一套 Apple root / SOCKS 传输配置。v2 保留 provider 的设备型号和 OS，不再硬编码另一个 Mac 型号覆盖它们。
- 即使 HTTP 400，仍读取 plist `ErrorID`、`protocolVersion` 和 `X-Apple-Reason-Code`。日志只保留结构化诊断，原始响应只在显式指定 dump 时保存。
- 顶层或 mobileme delegate 状态失败时拒绝返回令牌；修复空 `appleAccountInfo` 导致忽略 service-data DSID 的分支。
- worker 完成 GSA/2FA 后，先发已有的 account-side postdata，再换 iCloud delegate。此前只能在拿到 iCloud client 后注册，顺序与参考 DLL 相反。这没有复制参考客户端会改变 web access/security 状态的额外字段，也不把 postdata 接受视为 iCloud 激活。
- `delegatedebug` 和 `v2login` 共用探测实现，复用原 native profile 和真实保存的 APS token；不再打印令牌、DSID、anisette 值或完整服务器 body。
- native 稳定性测试只比较稳定身份/MID/LU，不再把动态 `X-Apple-I-MD` 当成必须跨重启不变的值。
- 增加显式 `APPLESERVICES_2FA_PROFILE=browser` 对照：只采用参考实现的浏览器 UA 和 Sec-Fetch 头，不更换 anisette 身份。此选项是实验入口，不能保证消除 423。

本地回归检查覆盖：V2 wire 键名、HTTP 400 诊断保留、不重试 PET、拒绝失败响应中的令牌、旧 marker、账号身份生成和 GSA/postdata 的前置条件。`go test ./...` 和 worker 构建通过。**本地测试不等于苹果服务器接受。**

## 4. 运行方式与诊断变量

Windows PowerShell 示例，密码只通过环境/项目面板传入，不写到命令行参数或文档。

```powershell
go build -o bin\trusted-device-gui.exe ./cmd/trusted-device

# 远程头；本地模块只负责验证码生成。设备身份沿用用户指定目录。
$env:APPLESERVICES_REMOTE_ANISETTE = '1'
$env:APPLESERVICES_NO_NATIVE_DEVICE_AUTH = '1'
$env:APPLESERVICES_DELEGATE_PATH = 'iosbuddy'
# APPLESERVICES_APPLEID / APPLESERVICES_PASSWORD 由用户提供。
.\bin\trusted-device-gui.exe -native-adi -background -control-api -gui-auth `
  -twofactor sms -dir state\accounts\01-account -anisette https://ani.sidestore.app `
  -adi-dll .cache\openbubbles-store\rust_lib_bluebubbles.dll -python python
```

`ani.sidestore.app` 是本轮实际 provision 成功的服务器；其他服务器的当前状态未在本轮确认。

| 变量 | 精确含义 |
| --- | --- |
| `APPLESERVICES_DELEGATE_PATH=iosbuddy/prefpane/v2/legacy-v2` | 明确选择单个协议实验；prefpane 的原生 body 尚未完成验证。 |
| `APPLESERVICES_REMOTE_ANISETTE=1` | worker 的 GSA/delegate 头改为远程 provider；native 模块仍负责生成六位码。 |
| `APPLESERVICES_NO_NATIVE_DEVICE_AUTH=1` | 不添加 worker 的 AppleIDSettings GSA 身份字段。 |
| `APPLESERVICES_DELEGATE_DUMP=<path>` | 显式保存原始响应，可能含敏感数据；不可发布。 |
| `APPLESERVICES_SOCKS_PROXY=host:port` | GSA 和 delegate 使用同一 SOCKS 出口；storage 等其他路径需独立核对。 |
| `APPLESERVICES_2FA_PROFILE=browser` | 明确比较参考客户端的 2FA 浏览器 UA/Fetch 头。 |
| `APPLESERVICES_ADI_RINFO=<n>` | 仅诊断覆盖数字，不会重算 provisioning 凭据。 |
| `APPLESERVICES_ICLOUD_SETUP=<url>` | 显式覆盖 setup host；不会自动推断账号区域。 |

旧 `APPLESERVICES_DELEGATE_AUTH=mme/mme-pet` 只存在于旧探针，不是 worker 的功能，本轮共用探针已移除此无有效对照支持的分叉。

## 5. 接下来如何完成验收

1. 先解决当前 GSA 的 native environment mismatch / 2FA 423。远程 SRP 已通过，应保持该账号和已取得的身份，不反复申请 SMS 或换账号污染对照。
2. 在同一个仍运行的 challenge 中提交验证码，确认 GSA 验证成功；再核验 postdata 和 delegate 的各自响应。
3. 使用新取得的有效 PET、正确 body 和一致出口验证 iosbuddy 或 legacy-v2。完整 v2 的 NAS 需求必须单独验证。
4. NAC 路径现在已有可复现的 Windows 工具：`tools/mac-nac-check.py`、`tools/mac-io-encrypt.c`、`tools/nacexchange`。它使用 OpenAbsinthe 的公开 x86_64 仿真和 Apple 的证书/会话接口。Windows 移植的 XNU 属性加密通过上游 5 组已知向量。当前实测使用了明确标注的合成 ROM/MLB；这证明协议流程和服务器响应，不证明这些合成硬件字段具有官方设备身份。
5. 成功标准：delegate 明确返回有效 `mmeAuthToken`，随后该 token 的只读配额请求成功。本轮两项均已满足。购买 iCloud+ 另需明确成功标记与购买后付费容量核验。

## 6. 参考来源

- [RustPush auth.rs](https://github.com/OpenBubbles/rustpush/blob/master/src/auth.rs)：`V2AuthRequest` 的 serde 声明、v2 body、NAS header。
- [headless legacy-body 兼容提交 0f5bb1f](https://github.com/stek29/rustpush/commit/0f5bb1f)：validation data 为空时选择 legacy body。其存在说明有其他协议路径，不证明本项目账号一定可用。
- [公开 OpenAbsinthe NAC 仿真实现](https://github.com/mosessupposeshis/imessage/blob/master/rustpush/open-absinthe/src/nac.rs)：真实 `new` / `key_establishment` / `sign` 及硬件资料依赖。
- [pypush NAC 实现](https://github.com/JJTech0130/pypush/blob/sms-registration/emulated/nac.py)：调用 Apple IMDAppleServices 的 NAC 三个入口，不是三个 `todo!()`。
- 本地校验过 hash 的 DLL 对应源码 `.cache/openbubbles-store/api.rs`：`do_login` 在 `login_apple_delegates` 前执行 `update_postdata`。

不要重新把候选原因写成最终结论。每次交接都分别记录：源码事实、本地回归结果、当次真实响应、仍待验证的解释。
