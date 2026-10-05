# 登录协议核查记录

核查日期：2026-10-05。范围仅为设备身份认证、Apple ID / 双重验证和 iCloud 登录令牌交换。

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
