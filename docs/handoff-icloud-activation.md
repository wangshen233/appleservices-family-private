# iCloud 激活交接文档

**日期**：2026-10-08
**目标**：让一个从未在真机登录过的 Apple 账号完成 iCloud 激活（拿到 `mmeAuthToken`），从而能购买 iCloud+。
**状态**：**未完成**。卡在最后一步 `loginDelegates`，苹果返回 `ICLOUD_INVALID_DEVICE_TOKEN`。

---

## 1. 问题是什么

购买 iCloud+ 需要账号被苹果认定为「iCloud 已激活」，唯一凭据是 `loginDelegates` 端点返回的 `service-data`（内含 `mmeAuthToken`）。

**当前**：该端点对**所有测试过的账号**都拒绝：

```xml
<key>dsid</key> <string>00000000000</string>      ← 账号识别成功（DSID 已替换为示例值）
<key>delegates</key>
  <key>com.apple.mobileme</key>
    <key>status</key> <integer>1</integer>
    <key>status-message</key> <string>A server problem is blocking Apple ID sign in. Try signing in later.</string>
<key>status</key> <integer>0</integer>            ← 顶层成功
```

**关键**：顶层 `status: 0` + 正确的 `dsid` 说明认证链是通的，**只有 mobileme 这一个 delegate 被单独拒绝**。

---

## 2. 最重要的线索：苹果在别的端点说了真话

`iosbuddy` 端点（项目当前用的）只回笼统的 "server problem"。**换端点就能拿到真实原因**：

| 端点 | 谁在用 | 响应 |
|---|---|---|
| `/setup/iosbuddy/loginDelegates` | 本项目 | `"A server problem..."`（**藏起原因**） |
| `/setup/prefpane/loginDelegates` | OpenBubbles DLL | **`ICLOUD_INVALID_DEVICE_TOKEN`** + `X-Apple-Reason-Code: 3005` |
| `/setup/signin/v2/login` | rustpush | **`3005`**，HTTP 400，0 字节 |

**`3005` / `ICLOUD_INVALID_DEVICE_TOKEN` = 设备令牌无效。**

复现命令（需要 `session.json` 里有新鲜的 PET）：
```bash
# prefpane 端点
curl -i -X POST "https://setup.icloud.com/setup/prefpane/loginDelegates" \
  -H "Content-Type: text/plist" \
  -H "X-Apple-ADSID: <DSID>" \
  -H "Authorization: Basic $(printf '%s:%s' '<appleid>' '<pet>' | base64 -w0)" \
  --data-binary @body.plist
# body.plist = { apple-id, delegates:{com.apple.mobileme:{}}, password:<pet>, client-id:<uuid> }
```

**`prefpane` 端点比 `iosbuddy` 更值得用** —— 它给真实原因，`iosbuddy` 不给。

---

## 3. 已排除的可能（全部有对照实验）

这些**都试过，都不是原因**，不要重复：

| 假设 | 实验 | 结果 |
|---|---|---|
| 密码错误 | GSA 完整登录 | ✅ 通过（注意 `<account-alias>` 的密码以**两个斜杠**开头，容易漏掉） |
| 账号问题 | 3 个不同账号 | 全部同样失败 |
| 设备身份被污染 | 每次用全新 16 字节随机身份 | 无差异 |
| anisette 来源 | 远程服务器 vs 本地 nativeadi | **完全无差异** |
| `X-Apple-I-MD-LU` 缺失 | 补齐（与远程 anisette 算法一致） | 无差异 |
| RINFO 值不对 | 覆盖为 `17106176` / `67437824` | 无差异 |
| `nativeDevice` 客户端身份 | 开关切换 | 无差异 |
| `termsAccepted` cookie | 去掉 | 无差异 |
| 出口 IP 被限流 | 走 SOCKS5 代理（日本出口） | 无差异 |
| 设备未向苹果注册 | 真实 provision（627 字节 `adi_pb`） | **无差异**（见下） |
| 重试不足 | 加 12–40 次退避重试 | 无差异 |
| `Xcode` 客户端头导致 423 | 去掉 | 解决 423，但不解决 delegate |

**结论：代码侧已全部排除。**

---

## 4. 已修复的真实缺陷（都已提交，有测试）

这些**是真 bug，值得保留**：

| commit | 内容 |
|---|---|
| `7f70a8e` | GSA/商店端点边缘随机断路（实测放行率 ~3%），加重试；补 `X-Apple-I-MD-LU`；验证码错误不再被吞 |
| `e1f0b47` | **`ProvisioningBlob` 占位符 bug** —— 设备身份从未向苹果注册 |

### 4.1 provision 占位符 bug 的细节（重要）

`internal/dashboard/identity.go` 曾写：

```go
ProvisioningBlob: []byte("native-adi"),   // 假占位符
```

而 `anisette/provider.go` 用「blob 非空」判断是否需要注册：

```go
if !p.state.Provisioned() {   // len(AdiPB) > 0
    cli.Provision(ctx, &p.state)   // ← 被假占位符跳过
}
```

**后果**：`provisionIdentity` 生成的所有身份从未真正注册。**这可能是 10-05 之后「从能用变成不能用」的原因**（旧代码不写这个字段，所以每次都会自动注册）。

修复后能拿到苹果颁发的真实凭证：

```bash
go build -o provisiondevice.exe ./tools/provisiondevice
./provisiondevice.exe <state-dir> https://ani.sidestore.app
# PROVISION OK
#   adi_pb len = 627
#   saved to device.json
```

**注意**：默认的 `ani.sidestore.io` **已坏**（`EndProvisioningError`），用 `ani.sidestore.app` 或 `ani.npeg.us` 或 `ani.idevicehacked.com`。

**但**：即使真实注册后，delegate 仍然被拒 —— 所以这不是唯一原因。

---

## 5. 未验证的核心假设（下一个 AI 应该从这里开始）

**`3005` 说的是「设备令牌」。我们的 anisette 来自远程服务器，它给的是通用凭证还是真实设备令牌？**

### 关键发现：远程 anisette 是官方路径

`OpenBubbles/apple-private-apis` 的 `omnisette/src/lib.rs`：

```rust
#[cfg(all(not(target_os = "macos"), feature = "remote-anisette-v3"))]
pub type DefaultAnisetteProvider = RemoteAnisetteProviderV3;   // ← 非 macOS 默认用远程
```

**所以「必须真机」的假设是错的** —— OpenBubbles 在 Windows 上也用远程 anisette。

### 但 rustpush 多带一个头

`rustpush/src/auth.rs:415`：

```rust
.header("X-Mme-Nas-Qualify", base64_encode(&validation_data))
```

`validation_data` 来自 `os_config.generate_validation_data()`：

```rust
// rustpush/src/macos.rs:93
let url = get_bag(IDS_BAG, "id-validation-cert");     // 证书
let url = get_bag(IDS_BAG, "id-initialize-validation"); // 会话协商
let mut ctx = ValidationCtx::new(&certs, &mut output_req, &self.inner)?;
ctx.key_establishment(&output)?;
Ok(ctx.sign()?)
```

**但 `open-absinthe` 是空实现**（`todo!()`）—— 连 rustpush 自己都没实现它。

`OpenBubbles/OpenAbsinthe-Stub` 里全是 `todo!()`，**所以这条线索可能走不通**。

**相关的 IDS bag**：
```
https://init.ess.apple.com/WebObjects/VCInit.woa/wa/getBag?ix=3
  → bag["id-validation-cert"]        = http://static.ess.apple.com/identity/validation/cert-1.0.plist
  → bag["id-initialize-validation"]  = https://identity.ess.apple.com/WebObjects/TDIdentityService.woa/wa/initializeValidation
```

验证证书（2385 字节，Apple 封装，DER 从 offset 6 开始）：
```
subject: Apple System Integration Certification Authority
notAfter: Jan 26 19:01:34 2019 GMT    ← 已过期
```

---

## 6. 环境与工具

### 6.1 关键路径

| 东西 | 位置 |
|---|---|
| 项目 | `C:\temp jiazi\apple 2\appleservices-main` |
| OpenBubbles DLL | `.cache/openbubbles-store/rust_lib_bluebubbles.dll`（40MB，SHA256 校验） |
| 已克隆的参考实现 | `C:\Users\27157\AppData\Local\Temp\apa`（apple-private-apis）、`...\rp2`（rustpush） |
| 状态目录 | `state/accounts/`（01-primary = <account-alias>，01-account = <account-alias>） |

### 6.2 环境变量开关（本次新增）

| 变量 | 作用 |
|---|---|
| `APPLESERVICES_REMOTE_ANISETTE=1` | 用远程 anisette 头（nativeadi 仍负责生成验证码） |
| `APPLESERVICES_NO_NATIVE_DEVICE_AUTH=1` | 关掉 AppleIDSettings 客户端身份 |
| `APPLESERVICES_DELEGATE_V2=1` | 用 `signin/v2/login` 端点 |
| `APPLESERVICES_DELEGATE_DUMP=<path>` | 保存 delegate 原始响应 |
| `APPLESERVICES_DELEGATE_AUTH=mme\|mme-pet` | 换用 X-MobileMe-AuthToken 认证 |
| `APPLESERVICES_SOCKS_PROXY=host:port` | GSA 走 SOCKS5 |
| `APPLESERVICES_ADI_RINFO=<n>` | 覆盖 RINFO |
| `APPLESERVICES_ICLOUD_SETUP=<url>` | 覆盖 setup 域名（中国区用） |

### 6.3 诊断工具（都是本次新增）

| 工具 | 用途 |
|---|---|
| `tools/delegatedebug` | 带真实 anisette 头调 delegate，打印**原始响应** |
| `tools/provisiondevice` | 向苹果注册设备身份，保存 `adi_pb` 到 `device.json` |
| `tools/gsacheck` | 只跑 GSA init，看身份是否被接受 |
| `tools/v2login` | 调 `signin/v2/login` 端点 |
| `tools/anisettecompare` | 对比远程/本地 anisette 的头 |
| `tools/spddump` | 打印会话里的服务令牌 |
| `tools/proxycheck` | 确认代理出口 IP |

### 6.4 运行完整登录

```bash
go build -o bin/trusted-device-gui.exe ./cmd/trusted-device

# 1. 生成并注册身份
mkdir -p /tmp/idN && python -c "
import base64, os, json
ident = os.urandom(16)
json.dump({'Identifier': base64.b64encode(ident).decode(),
           'ProvisioningBlob': '', 'SerialNumber': 'AS'+ident[:5].hex().upper()},
          open('/tmp/idN/device.json','w'), indent=2)"
./provisiondevice.exe /tmp/idN https://ani.sidestore.app

# 2. 登录（会停在 awaiting_sms）
APPLESERVICES_DELEGATE_DUMP=/tmp/dump.plist \
APPLESERVICES_APPLEID='<账号>' APPLESERVICES_PASSWORD='<密码>' \
./bin/trusted-device-gui.exe -native-adi -background -control-api -gui-auth \
  -twofactor sms -dir /tmp/idN \
  -adi-dll ./.cache/openbubbles-store/rust_lib_bluebubbles.dll -python python

# 3. 提交验证码
curl -X POST "http://127.0.0.1:<port>/action" \
  -H "Content-Type: application/json" \
  -H "X-Control-Token: <control.json 里的 token>" \
  -d '{"action":"submit-sms","code":"<6位>"}'
```

---

## 7. 已知的坑

1. **`ani.sidestore.io` 已坏** —— 用 `ani.sidestore.app`。探测可用服务器：`curl https://servers.sidestore.io/servers.json`
2. **423 Locked** —— 同一账号短时间内反复请求验证码会被限。换账号或等待。`Xcode` 客户端头会加剧这个问题（已移除）。
3. **PET 是一次性的** —— 每次 delegate 调用消耗一个 PET，重复用同一个 session 测会得到误导性的 `"password entered incorrectly"`。
4. **验证码必须及时提交** —— worker 重启会重置 challenge。
5. **`-native-adi` 会绕过远程 anisette** —— 测试 provision 修复时必须加 `APPLESERVICES_REMOTE_ANISETTE=1`，否则 `adi_pb` 根本不被使用。
6. **`boot_args` 页面 423** —— 用 `-twofactor trusted-device` 可绕过，但那个通道需要账号已有可信设备（本场景没有），所以**还是要用 sms**。

---

## 8. 建议的下一步

按优先级：

1. **用 `prefpane` 端点替代 `iosbuddy`** —— 至少能拿到真实错误原因，不再瞎猜。改动很小（`icloud/delegate.go` 换个 URL + body 格式）。
2. **验证 `ani.sidestore.app` 是否真的在给有效设备令牌** —— 换几个服务器对比（`ani.npeg.us`、`ani.idevicehacked.com`）。
3. **查 `X-Mme-Nas-Qualify` 是否是必需** —— rustpush 带了，但 `open-absinthe` 是 stub；确认 OpenBubbles DLL 是否真的发这个头（DLL 字符串里有，说明**发**）。
4. **考虑用真机** —— 如果有 iPhone/Mac，`apple-private-apis` 的 `clearadi` 能从真机提取 ADI。或者直接在真机上登录一次该账号，让苹果认识这个设备。

**最后手段**：如果所有软件路径都不通，**在真机 iPhone 上登录一次该账号**是最可靠的激活方式 —— 登录后苹果就会为该账号建立 iCloud 状态，之后 Windows 侧只需维持会话。
