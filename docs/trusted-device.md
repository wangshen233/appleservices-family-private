# 受信任设备与验证码接收

核查日期：2026-10-05。目标是这台 Windows 上的程序成为账号的验证码接收设备。

## 当前结果

**设备登记请求已被 Apple 接受，已实际收到新登录的 AuthKit 请求；尚不能生成并验证六位码。**
Windows 原生推送路径已走通，剩余核心缺口是与登录身份一致的六位码生成组件。

真实账号验证结果：

- 密码认证和 iCloud 令牌交换继续成功；没有重新要求验证码。
- 账号设备查询返回 3 个现有设备，未输出设备名称、序列号或账号编号。
- 不带有效 APS 令牌的 `postdata` 被拒绝：HTTP 401，`ec=-800012`，`Push token is invalid.`。
- 根据这个结果，登记 API 已改为要求 32 字节 APS 令牌；CLI 只使用 APS 成功认证返回的令牌。
- 本机已安装 iCloud 15.8.118.0。调用已注册的 `IAPSNotificationServer`，获得真实 32 字节令牌。
- 带该令牌重新登录并调用 `postdata` 成功，设备列表中出现与登记名称一致的条目。
  设备总数仍为 3，不能据此声称新增了第 4 台设备或已完成验证码能力验证。
- 订阅 `com.apple.idmsauth` 后，收到当前账号的命令 1200（设备状态变化）。
- 独立创建新登录身份，密码认证进入双重验证，请求受信任设备验证码成功；
  监听程序随后收到该账号的命令 100（登录请求）和命令 400（取消）。
- 本机 `CoreADI64.dll` 初始化成功，但公开定义的六位码调用 `0x385b2ce8` 返回 `-45019`（调用未实现），没有产生验证码。
- 没有伪造注册成功，没有恢复钥匙串、修改云端访问权限或删除原有设备。

序列号曾固定为 `0`。现支持安装级稳定序列号，并将它保存在已有 `state/device.json` 中。
新的身份字段不会重新生成 anisette 的 16 字节标识或 provisioning blob。

## 已写入项目

1. `aps/`：导入匹配的客户端证书和 RSA 私钥，验证证书有效期；保持 Apple 服务器 TLS 验证。
2. APS `apns-security-v3` 长连接：签名认证，获取并保存真实推送令牌，设置连接状态，订阅 `com.apple.idmsauth`。
3. APS 帧边界和大小限制、通知确认、心跳，以及命令 100 的登录请求和命令 400 的取消事件。
4. `gsa`：使用 heartbeat token 调用 `postdata`，携带设备名称和推送令牌，读取账号设备状态。
5. SRP 的 CPD 可绑定推送令牌，并支持用于设备登记的本机登录上下文。
6. `teardown` 使用上游实际调用的 `defbtn` / `albtn` 动作响应允许或拒绝。
7. CLI 只处理当前账号的请求，显示验证码前需要用户输入 `allow`，会检查请求是否已取消。
8. `aps.NativeClient`：通过嵌入的 C# COM 桥接调用本机 Apple 推送服务，订阅及取消订阅仅涉及本连接的 IDMS 主题。
   桥接进程使用 Windows 自带 PowerShell 的 MTA 模式；账号密码不传给桥接进程，不导出 Apple 的证书或私钥。
9. 已根据真实通知修复 `aps.alert` 的字符串/对象兼容；普通未处理通知不会中断连接，命令 700 仍明确报错。

以上协议传输的本地测试覆盖签名验证、握手、订阅、通知确认、输入截断、证书密钥不匹配和 Apple 拒绝登记的错误解析。
本地模拟服务不能证明 Apple 接受受信任设备注册。
命令 700 的 Circle/PAKE 配对流程尚未实现；收到该命令会明确报错，不会冒充已经完成配对。

## 推送凭据与剩余生成组件

### 客户端推送证书

APS 需要 Apple 签发的 **client push certificate** 和匹配私钥。
Apple Developer 后台用于向应用发送通知的 APNs provider 证书不是这个凭据。
仅凭账号密码不能在本项目中产生该证书；普通 anisette provisioning 也没有返回它。

已检查的公开 Albert 实现还需要签名的 activation 数据。
本项目没有嵌入网上公开的共享 FairPlay 私钥，也没有可用的本机签名器；
证书直连入口支持导入已取得的客户端证书，不声称已实现它的自动签发。

这台 Windows 已有 Apple 的 APS 服务，因而新增 `-native-push` 路径：
Apple 服务负责自己的客户端凭据和连接，程序只取得令牌及订阅通知，无需导出证书和私钥。
旧 `ApplePushService.dll` 使用的 `NotificationCenter2` COM 类未对包外程序注册；
改用安装包已公开注册的 `APSNotificationServer` / `NotificationCenter` 接口后连接及本地回调实测成功。

### ADI 验证码生成

AuthKit 登录请求推送通常包含请求信息和事务编号，不能假定其中有明文六位验证码。
上游收到请求后调用本机 anisette/ADI provider 的 `get_2fa_code()`。

检查结果：

- `OpenBubbles/clearadi-stub` 的 `gen_2fa_code()`、provisioning 和 OTP 函数均为 `todo!()`。
- `omnisette` 的默认 `get_2fa_code()` 返回 `MissingLibraries`；SideStore v3 provider 未实现该函数。
- `Dadoum/Provision` 的 `ADIGetLoginCode` 是 provisioning 状态检查，返回 0 表示已 provisioning。
  不能把它的 0 格式化成 `000000` 当作验证码。
- `ani.sidestore.io` 的公开 v3 接口提供认证头和 provisioning，不提供可调用的验证码生成接口。
- 新查到的 `Dadoum/apple-crates` 定义了真正的六位码调用，区别于 `ADIGetLoginCode` 状态检查。
  调用已在本机 DLL 上复现：初始化成功，六位码调用返回 `-45019`。未枚举本机其他账号、修改本机 provisioning，或输出验证码。

`aps.CodeFromProcess` 是连接一个已实现的本地生成器的接口，**不是验证码算法的实现**。
正常收码模式缺少生成器时会报错；显式使用 `-listen-only` 可登记及观察通知，不能允许请求或显示验证码。

## Windows 实验命令

本机原生推送探测，不需要账号密码，不提交设备登记：

```powershell
go build -o .\bin\trusted-device.exe ./cmd/trusted-device
.\bin\trusted-device.exe -probe-native-push
```

已实测返回令牌长度 32，私有本地 COM 回调通过。这个回调测试不代表远端验证码投递。

只观察真实通知（沿用已有登录状态与环境变量中的账号密码）：

```powershell
.\bin\trusted-device.exe -native-push -listen-only -listen-for 2m
```

这个命令会登录、向账号提交登记、查询设备状态并监听通知；不会批准登录请求或生成验证码。
不指定 `-listen-for` 时持续监听，Ctrl+C 退出。桥接退出时取消自己的订阅和回调，不停止 Apple 推送服务。

准备好与登录 anisette 身份一致的真实生成器后，正常收码模式可用 `-native-push -code-provider ...`。
证书直连模式仍需要真实客户端推送证书、对应私钥和生成器：

```powershell
go build -o .\bin\trusted-device.exe ./cmd/trusted-device
.\bin\trusted-device.exe -check `
  -push-cert 'C:\path\client-push-cert.pem' `
  -push-key 'C:\path\client-push-key.pem' `
  -code-provider 'C:\path\adi-code-provider.exe'
```

`-check` 只检查本地输入，不访问 Apple，不证明设备受信任。
去掉 `-check` 会建立 APS 连接、用环境变量中的账号密码认证、提交登记请求并监听通知。
账号密码参数使用已有 `APPLESERVICES_APPLEID` / `APPLESERVICES_PASSWORD`，不写进状态文件。

生成器通过标准输入接收 JSON：`device_id`、Base64 的 `identifier` 和 `adi_pb`。
它必须返回 JSON：同一 `device_id` 和六位字符串 `code`。不向它传递账号密码或 iCloud 令牌。
生成器路径必须是绝对路径，执行限时，输出限长，不经过 shell。

证书直连模式的 `state/push.json` 含客户端推送私钥、证书和令牌，位于已有 Git 忽略目录。
原生模式每次从 Apple 服务取得令牌，不把服务持有的证书或私钥保存到该文件。
收到登录请求后输入 `allow` 才显示验证码；`deny` 拒绝，其他输入忽略。
程序没有自动批准所有请求，也没有把接收的通知内容写入普通日志。

## 最终验收仍需完成

1. Apple 接受真实 APS 凭据与推送令牌。
2. Apple 账号设备页面显示当前安装，且明确允许它接收验证码。
3. 另一个新的登录会话触发验证码请求。
4. 本程序收到对应请求并生成验证码，该新会话提交后被 Apple 接受。
5. 重启后保持相同身份，重新连接仍能完成上述接收。

目前已通过原生令牌、登记请求与一次新登录的真实请求接收。
账号页面明确的收码能力、生成六位码后被 Apple 接受，以及完整流程重启复验仍未完成。

六位码调用的可复现实测工具为 `tools/probe-native-adi.py`。传入本机安装的、已暂存到工作区的 DLL 绝对路径：

```powershell
python .\tools\probe-native-adi.py 'C:\path\CoreADI64.dll'
```

此工具只探测已知接口，不是生成器。当前 DLL 返回 `six_digit_generation_supported=false`，退出码 2。

## 公开依据

- [RustPush 的 APS 与 AuthKit 消息](https://github.com/OpenBubbles/rustpush/blob/master/src/auth.rs)：`IdmsAuthListener`、`com.apple.idmsauth`、命令 100 / 400。
- [Apple 私有 API 的登记请求](https://github.com/OpenBubbles/apple-private-apis/blob/master/icloud-auth/src/client.rs)：`update_postdata` 与 `teardown`。
- [登录 CPD 与请求头](https://github.com/OpenBubbles/apple-private-apis/blob/master/icloud-auth/src/anisette.rs)：本机应用上下文与 `ptkn`。
- [ClearADI 的公开占位实现](https://github.com/OpenBubbles/clearadi-stub/blob/main/src/lib.rs)：验证码生成函数未实现。
- [ADI provider 能力](https://github.com/OpenBubbles/apple-private-apis/blob/master/omnisette/src/lib.rs)：默认缺少验证码生成能力。
- [Provision 状态检查](https://github.com/Dadoum/Provision/blob/master/lib/provision/adi.d)：`isMachineProvisioned` 的返回值含义。
- [SideStore v3 服务](https://github.com/Dadoum/anisette-v3-server/blob/master/source/app.d)：公开的 provisioning 与认证头接口。
- [OpenBubbles 对允许/拒绝按钮的调用](https://github.com/OpenBubbles/openbubbles-app/blob/rustpush/android/app/src/main/kotlin/com/bluebubbles/messaging/services/rustpush/AuthCodeActivity.kt)：`defbtn` / `albtn`。
- [ADI 六位码消息与调用编号](https://github.com/Dadoum/apple-crates/blob/master/adi/src/calls.rs)：`Gen2FACodeMessage`、`0x385b2ce8`。
- [ADI 调用参数序列化](https://github.com/Dadoum/apple-crates/blob/master/adi/src/core_adi.rs)：明文标记、固定长度大端字段和独立输出指针。
- [ADI 错误含义](https://github.com/Dadoum/apple-crates/blob/master/adi/src/proxy.rs)：`-45019` 是 `InvalidFunctionCode`，区别于未 provisioning 的 `-45061`。
- 原生 COM 接口来自本机 iCloud 15.8 包中的 `APSDaemon_main.dll` 类型库及 `AppxManifest.xml`，不从未知地址取得 Apple 二进制。

源码用于核对协议字段。没有复制上游共享私钥或声称占位生成器可以工作。
