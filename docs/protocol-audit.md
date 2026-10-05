# 登录协议核查记录

核查日期：2026-10-05。范围仅为设备身份认证、Apple ID / 双重验证和 iCloud 登录令牌交换。

最新实际结果：用户授权账号已完成 GrandSlam 认证、短信双重验证、iCloud delegate 令牌交换和会话保存。
以下各节按排查过程保留之前的失败与修复记录；末节是最终验证结果。

## 上游时间与已确认的变化

GitHub API 显示，原项目最近一次 GSA 修改是 2026-07-27，提交
[`37a93e3`](https://github.com/Laky-64/appleservices/commit/37a93e3069cfbd62bfcc25e0c5057b23e732738f)。
仓库整体最近推送时间为 2026-08-07。

9 月出现了客户端标识兼容问题：GSA 请求携带旧 `com.apple.dt.Xcode` bundle 标识时，
上游报告会被 Apple 返回 HTTP 503。相关修复已落地到其他客户端和 anisette 服务：

- [isideload PR #11](https://github.com/nab138/isideload/pull/11)：替换为 `com.apple.akd/1.0`；该 PR 已合并。
- [anisette-v3-server PR #60](https://github.com/Dadoum/anisette-v3-server/pull/60)：对服务输出的客户端信息应用同样修复。
- [AltStore PR #1790](https://github.com/altstoreio/AltStore/pull/1790)：原始问题的隔离过程与修复方案；该 PR 的状态为关闭，不标成已合并。

本次只改旧 bundle token，保留 anisette 服务提供的硬件、系统和其余客户端信息。
处理覆盖 provisioning、GrandSlam HTTP 头与 CPD、双重验证和 iCloud delegate 交换。
外部自定义 anisette provider 也会经过处理。

本次查询 `https://ani.sidestore.io/v3/client_info` 时，默认服务已经输出 `com.apple.akd/1.0`。
因此补丁主要解决较旧服务或自定义 provider 的兼容问题，不能据此认定默认配置之前必然登录失败。

## SRP 与请求格式

现有算法和 [cocagne/pysrp](https://github.com/cocagne/pysrp/blob/master/srp/_pysrp.py) 在
RFC 5054 模式、`no_username_in_x` 模式下的计算一致。
没有发现需要更改 SRP 核心数学计算的证据。

用其独立客户端和服务端生成并相互验证了两组仅含合成数据的测试向量：

- `s2k`：SHA-256 摘要原始字节进入 PBKDF2。
- `s2k_fo`：SHA-256 摘要的十六进制 ASCII 进入 PBKDF2；该向量的公开值 A 仅占一个字节，验证补齐与非补齐的区别。
- 两组均核对 password key、x、A、session key、M1 和 M2；还验证篡改 M2 会被拒绝。

参考源码下载文件的 SHA-256：
`574a00066d8023a34291c504dcf9a3ca24dd8f8aef1ac6623416403c2b9c6064`。
向量保存在 `internal/srp/testdata/apple_srp.json`，测试不需要 Python 或网络。

新增明确的未知 SRP 协议和未知认证后续流程错误，防止服务端变化时按旧协议继续提交。

## 返回格式和错误处理

- 区分 GSA HTTP 429/503 等拒绝与 plist 中的 Apple `ec` 错误，保留 init / complete 阶段和选用协议。
- 支持 `Status` 内和响应顶层的 `ec`；不把所有 complete 失败描述成密码错误。
- 原始 `em` 留在结构化错误对象中，默认错误字符串只显示阶段和代码，避免泄露账号信息。
- 受信任设备验证码响应即使 HTTP 200，也会校验非空 plist 的错误码。
- 短信元数据兼容 JSON `trustedPhoneNumbers` 和 HTML `boot_args`，提交时复用请求验证码时选中的号码 ID。
- 相关返回格式参考 [OpenTagViewer 认证研究](https://github.com/parawanderer/OpenTagViewer/blob/main/docs/findmy-export/01-authentication.md)，
  本地测试覆盖 JSON / HTML 两条路径；该研究自身说明短信路径未做真实账号验证。

## 无凭据在线探测与验证边界

使用项目自身 `AppleRootTransport` 做了两次 POST 探测。请求体仅为 `t`，
不含账号、密码、验证码、anisette OTP 或会话；没有提交真实登录。

| 客户端 bundle 标识 | HTTP | 响应类型 | 响应字节数 |
| --- | --- | --- | --- |
| 旧 Xcode | 503 | text/html | 190 |
| akd | 503 | text/html | 190 |

此结果不能复现上游报告的不同状态，也不能区分无效请求、网络出口策略或其他服务限制。
没有足够证据认定仅替换 token 就能恢复当前真实登录。
探测保持证书验证开启，未改系统证书，也未通过禁用 TLS 验证绕过错误。

已通过本地协议序列化、响应解析、SRP 参考向量和登录状态机测试。
未使用真实账号测试 GrandSlam、可信设备 / 短信验证码或 iCloud delegate 登录。
实际可用性仍需由账号持有人运行 `run-login.ps1` 完成验证；如失败，阶段和错误代码可用于进一步定位。

## 实际尝试时的网络修复

同日实际尝试时，程序在 anisette 初始化所需的 Apple URL bag 请求阶段遇到本机连接拒绝，
尚未完成账号认证。发现 `AppleRootTransport` 自建的传输没有配置环境代理，
而运行环境需要使用代理。

现已从 Go 默认传输克隆配置并使用 `ProxyFromEnvironment`，保留 Apple 根证书及 TLS 验证。
修复后的无凭据 `GET /grandslam/GsService2/lookup` 返回 HTTP 200、XML plist、25,582 字节。
这只验证设备初始化接口可达，不代表账号、密码或双重验证已通过。

之后的登录执行被自动审批阻止，理由是向第三方 anisette 服务发送设备标识和 provisioning
数据需要明确授权；在取得授权前没有继续该尝试。

用户随后明确授权使用该服务，继续进行了一次实际认证：设备 provisioning 完成，
GrandSlam `init` 返回了可解析的 SRP 挑战；`complete` 请求返回 HTTP 429。
没有得到服务端 M2 / SPD，也未进入双重验证码或 iCloud delegate 交换。
因此账号密码正确性和实际 iCloud 登录仍未验证，未保存可信会话。
为避免连续提交被限流的请求，没有继续重试。
该记录不包含账号、密码、设备标识或 provisioning 数据。

## complete 返回 429 的原因与修复

之前将所有 HTTP 429 直接解释为频率限流不够准确。
对照近期上游后发现，与实际现象吻合的是 GSA 连接复用兼容问题：
第一条 `init` 得到响应，第二条 `complete` 在同一条连接上被返回 429。

- [iLoader v2.3.3](https://github.com/nab138/iloader/releases/tag/v2.3.3)：9 月 10 日发布，说明通过关闭 reqwest 连接池缓解 GSA 429。
- [FindMy.py PR #3](https://github.com/parawanderer/FindMy.py/pull/3)：已合并，包含真实登录复现与连接复用隔离。
- [后续简化提交 1471a7c](https://github.com/parawanderer/FindMy.py/commit/1471a7c)：按 GSA 主机关闭连接复用，确认桌面平台也受影响。

检查当前代码确认：虽然 HTTP 辅助库每次构造一个新的 `http.Client`，
它仍把同一个 `Transport` 传给 init / complete，底层连接池因而被共享。

新增仅针对 `gsa.apple.com` 的认证传输：HTTP/1、禁用 keep-alive、每个请求结束后关闭连接。
其他主机继续使用普通连接池；不更换账号、设备标识、anisette 服务或代理出口。
Cookie 仍由同一个 CookieJar 保存和传递，TLS 证书验证保持开启。

本地 TLS 回归服务模拟每条连接只能处理一次请求：
旧连接池复现第二次请求 429；新传输两次均成功，且 complete 仍收到 init 设置的 Cookie。
测试同时覆盖上游 HTTP/2 支持，确保 GSA 不再复用 HTTP/2 连接。

实际验证保持最早提供的账号密码、同一设备身份和同一代理：
init / complete 均通过，M2 校验成功，SPD 成功解密，Apple 返回双重验证要求。
程序已请求受信任设备验证码，目前等待输入。
这确认本次 429 是连接复用导致的兼容失败；仍不能把其他场景的所有 429 都解释为同一问题。
尚未完成双重验证和 iCloud delegate 交换，因此不把这一步描述为完整 iCloud 登录成功。

## 验证码后的登录状态修复

用户实际收到短信验证码，而前次程序使用受信任设备入口提交。返回后强制重做密码握手，
再次得到双重验证要求，因此完整登录仍未完成。不能凭程序请求的入口断言实际送达渠道。

对照上游发现两处遗漏：

1. 短信请求和提交应该在 `gsa.apple.com/auth/verify/phone` 与其 `securitycode` 子路径进行，
   原代码混用了另一套 IDMSA 地址。现已修复。
2. 两种验证码提交响应都可能直接在 `X-Apple-PE-Token` / `X-Apple-GS-Token` / `X-Apple-HB-Token`
   响应头中返回登录令牌。原代码忽略这些头，并无条件重做密码握手，可能再次触发验证。

现在收集所有重复令牌头；PET 使用固定服务名归档，不误把其首段账号编号当服务名。
在已验证的代码响应中得到 PET 时，沿用同一账号的 SRP payload，直接完成 iCloud delegate 交换。
只有未得到 PET 时，才沿用原来有上限的一次重新认证。令牌不输出到日志，也不额外保存到磁盘。

短信号码发现还支持嵌套 `trustedPhoneNumbers`，以及 HTTP 201 的“服务端已经自动发码”情形；
201 时跳过再次 PUT 发码，避免让用户收到两条不同验证码。

这些改动已通过本地测试，覆盖重复令牌头、PET 归档、保持账号绑定、跳过重复认证和短信自动发送。
短信路径的实际执行已经成功请求新验证码，当前等待用户输入。

## 最终实际验证结果

用户提供新短信验证码后，GSA 短信验证完成，程序进入 iCloud delegate 登录，
返回 `iCloud login successful. Trusted session saved.`。
这次实际执行完整覆盖密码 SRP 认证、验证码请求与提交、认证令牌传递、
iCloud 登录令牌获取、绑定账号的本地会话保存。

验证码、账号密码、Cookie 和令牌值没有写入本记录。
设备身份与会话文件位于已被 Git 排除的 `state/` 目录；会话文件不包含密码字段。
没有执行钥匙串恢复、密码读取、联系人读取或其他后续数据操作。
尚未验证其他账号，也未额外重复登录来测试后续是否免验证码。
