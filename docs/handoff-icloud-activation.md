# iCloud 激活交接文档

**日期**：2026-10-08
**目标**：让一个从未在真机登录过的 Apple 账号完成 iCloud 激活（拿到 `mmeAuthToken`），从而能购买 iCloud+。
**状态**：**未完成**。根因已定位：苹果要求**设备证明**（NAS / `X-Mme-Nas-Qualify`），当前所有 anisette 方案都不提供它。

---

## 0. 一句话结论

苹果对 `loginDelegates` 返回 **`ICLOUD_INVALID_DEVICE_TOKEN`**（"Cannot verify the identity of this Mac"）。
**问题不在账号、密码、请求格式或代码** —— 苹果要求一个「设备证明」头 `X-Mme-Nas-Qualify`，而它的生成算法**没有开源实现**（连 rustpush 自己都是 `todo!()`）。

**最可靠的解法**：在一台真实 iPhone/Mac 上登录一次该账号。之后苹果就认识这个设备，问题消失。

---

## 1. 问题的准确定义

购买 iCloud+ 需要账号被认定为「iCloud 已激活」，唯一凭据是 `loginDelegates` 返回的 `service-data`（内含 `mmeAuthToken`）。

**默认端点（`iosbuddy`）的响应**：

```xml
<key>dsid</key> <string>00000000000</string>      ← 账号识别成功（DSID 已替换）
<key>delegates</key>
  <key>com.apple.mobileme</key>
    <key>status</key> <integer>1</integer>
    <key>status-message</key> <string>A server problem is blocking Apple ID sign in. Try signing in later.</string>
<key>status</key> <integer>0</integer>            ← 顶层成功
```

**顶层 `status: 0` + 正确 `dsid`** 说明认证链是通的，**只有 mobileme 这一个 delegate 被单独拒绝**。

---

## 2. 根因：苹果说了真话，但不在默认端点上

`iosbuddy` 端点只回笼统的 "server problem"。**换端点就能拿到真实原因**：

| 端点 | 谁在用 | 响应 |
|---|---|---|
| `/setup/iosbuddy/loginDelegates` | 本项目默认 | `"A server problem..."`（**藏起原因**） |
| `/setup/prefpane/loginDelegates` | OpenBubbles DLL | **`ICLOUD_INVALID_DEVICE_TOKEN`** |
| `/setup/signin/v2/login` | rustpush | **`X-Apple-Reason-Code: 3005`** |

**`prefpane` 的完整响应**（这是本次调查最有价值的产物）：

```xml
<key>ErrorID</key>        <string>ICLOUD_INVALID_DEVICE_TOKEN</string>
<key>localizedError</key> <string>ICLOUD_INVALID_DEVICE_TOKEN</string>
<key>description</key>    <string>Cannot verify the identity of this Mac</string>
<key>Body</key>           <string>You cannot sign in to iCloud because there was a problem
                                  verifying the identity of this Mac. Try restarting your
                                  Mac and signing in again.</string>
<key>Header</key>         <string>Cannot sign in to iCloud.</string>
<key>protocolVersion</key><string>2</string>
```

**已实现**：`APPLESERVICES_DELEGATE_PATH=prefpane` 可切换到该端点（见 §6.2）。**诊断时务必用它**。

---

## 3. 已排除的可能（全部有对照实验，不要重复）

| 假设 | 实验 | 结果 |
|---|---|---|
| 密码错误 | GSA 完整登录 | ✅ 通过 |
| 账号问题 | 3 个不同账号 | 全部同样失败 |
| 设备身份被污染 | 每次全新 16 字节随机身份 | 无差异 |
| anisette 来源 | 远程服务器 vs 本地 nativeadi | **完全无差异** |
| anisette 服务器本身 | `ani.sidestore.app` / `.npeg.us` / `.idevicehacked.com` | 全部 `PROVISION OK`，RINFO 都是 `17106176` |
| `X-Apple-I-MD-LU` 缺失 | 补齐（与 omnisette 算法一致） | 无差异 |
| RINFO 值不对 | 覆盖为 `17106176` / `67437824` | 无差异 |
| `X-Apple-Locale` 缺失 | 补齐 | 无差异 |
| `X-Apple-I-TimeZone` = GMT | 改为 UTC | 无差异 |
| `nativeDevice` 客户端身份 | 开关切换 | 无差异 |
| `termsAccepted` cookie | 去掉 | 无差异 |
| 出口 IP 被限流 | SOCKS5 代理（日本出口） | 无差异 |
| 设备未向苹果注册 | 真实 provision（627 字节 `adi_pb`） | 无差异（但**是真 bug**，见 §5） |
| 重试不足 | 加 12–40 次退避重试 | 无差异 |
| `Xcode` 客户端头 | 去掉 | 解决 `423`，但不解决 delegate |
| 端点选择 | iosbuddy / prefpane / v2 | 全部拒绝 |
| 请求体键名 | `client-id`（kebab-case）修正 | 无差异 |

**结论：代码侧已彻底排除。**

---

## 4. 剩下的唯一嫌疑：`X-Mme-Nas-Qualify`

**这是本次调查的最终结论。**

证据：

1. **rustpush 发送它**（`src/auth.rs:415`）：
   ```rust
   .header("X-Mme-Nas-Qualify", base64_encode(&validation_data))
   ```

2. **能工作的 OpenBubbles DLL 发送它** —— DLL 字符串里明确有：
   ```
   Cookie Accept-Encoding gzip com.apple.iCloudHelper/282
   X-Mme-Client-Info X-Mme-Nas-Qualify X-Apple-ADSID
   ```

3. **本项目完全不发送它。**

4. **但它的生成算法没有开源实现**：
   - rustpush 调用 `os_config.generate_validation_data()`（`src/macos.rs:93`）
   - 实现来自 `open-absinthe` crate
   - **`OpenBubbles/OpenAbsinthe-Stub` 里全是 `todo!()`**

### 生成流程（来自 rustpush 源码，未实现部分）

```rust
// rustpush/src/macos.rs:93
let url = get_bag(IDS_BAG, "id-validation-cert");
let certs = /* 下载证书 */;
let mut ctx = ValidationCtx::new(&certs, &mut output_req, &self.inner)?;  // ← todo!()
let url = get_bag(IDS_BAG, "id-initialize-validation");
let activation = REQWEST.post(url).body(info).send().await?;              // 会话协商
ctx.key_establishment(&output)?;                                          // ← todo!()
Ok(ctx.sign()?)                                                           // ← todo!()
```

### 相关端点（已实测可达）

```
IDS bag:  https://init.ess.apple.com/WebObjects/VCInit.woa/wa/getBag?ix=3
  bag["id-validation-cert"]       = http://static.ess.apple.com/identity/validation/cert-1.0.plist
  bag["id-initialize-validation"] = https://identity.ess.apple.com/WebObjects/TDIdentityService.woa/wa/initializeValidation
```

**注意**：`id-validation-cert` 走 **HTTP**（HTTPS 被墙），返回 3474 字节。
证书是 Apple 封装格式（前缀 `010200000416`，**DER 从 offset 6 开始**），解码后：

```
subject:  Apple System Integration Certification Authority
issuer:   Apple Root CA
notAfter: Jan 26 19:01:34 2019 GMT    ← 已过期
```

**`HardwareConfig` 结构**（`open-absinthe/src/nac.rs`，说明需要哪些硬件字段）：

```rust
pub struct HardwareConfig {
    pub product_name: String,              // 如 "MacBookPro18,3"
    pub io_mac_address: [u8; 6],
    pub platform_serial_number: String,
    pub platform_uuid: String,
    pub root_disk_uuid: String,
    pub board_id: String,
    pub os_build_num: String,
    pub platform_serial_number_enc: Vec<u8>,   // 加密后的
    pub platform_uuid_enc: Vec<u8>,
    pub root_disk_uuid_enc: Vec<u8>,
    pub rom: Vec<u8>,
    pub rom_enc: Vec<u8>,
    pub mlb: String,
    pub mlb_enc: Vec<u8>,
}
```

---

## 5. 已修复的真实缺陷（都已提交，有测试）

这些**是真 bug，值得保留**：

| commit | 内容 |
|---|---|
| `7f70a8e` | GSA/商店端点边缘随机断路（实测放行率 ~3%），加重试；补 `X-Apple-I-MD-LU`；验证码错误不再被吞 |
| `e1f0b47` | **`ProvisioningBlob` 占位符 bug** —— 设备身份从未向苹果注册 |
| `545219a` | 交接文档 + 诊断工具 |
| `df49de6` | 补 `X-Apple-Locale`，timezone 改 UTC |
| `aa2559a` | delegate 端点可选，`prefpane` 报真实原因 |

### 5.1 provision 占位符 bug（重要）

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

**后果**：所有生成的身份从未真正注册。**这可能是 10-05 之后「从能用变成不能用」的原因**（旧代码不写这个字段，所以每次都会自动注册）。

修复后：

```bash
go build -o provisiondevice.exe ./tools/provisiondevice
./provisiondevice.exe <state-dir> https://ani.sidestore.app
# PROVISION OK
#   adi_pb len = 627
#   saved to device.json
```

**注意**：默认的 `ani.sidestore.io` **已坏**（`EndProvisioningError`）。可用服务器：
`ani.sidestore.app`、`ani.npeg.us`、`ani.idevicehacked.com`。
探测：`curl https://servers.sidestore.io/servers.json`

**但**：即使真实注册后 delegate 仍被拒 —— 所以这不是唯一原因，**根因仍是 §4 的 `X-Mme-Nas-Qualify`**。

---

## 6. 环境与工具

### 6.1 关键路径

| 东西 | 位置 |
|---|---|
| 项目 | `C:\temp jiazi\apple 2\appleservices-main` |
| OpenBubbles DLL | `.cache/openbubbles-store/rust_lib_bluebubbles.dll`（40MB，SHA256 校验） |
| 参考实现（已克隆） | `%TEMP%\apa`（apple-private-apis）、`%TEMP%\rp2`（rustpush）、`%TEMP%\oa`（OpenAbsinthe-Stub） |
| 状态目录 | `state/accounts/`（账号映射保存在本地私有状态中） |

### 6.2 环境变量开关（本次新增）

| 变量 | 作用 |
|---|---|
| `APPLESERVICES_DELEGATE_PATH=prefpane\|v2` | **切换 delegate 端点（诊断首选 prefpane）** |
| `APPLESERVICES_REMOTE_ANISETTE=1` | 用远程 anisette 头（nativeadi 仍生成验证码） |
| `APPLESERVICES_NO_NATIVE_DEVICE_AUTH=1` | 关掉 AppleIDSettings 客户端身份 |
| `APPLESERVICES_DELEGATE_DUMP=<path>` | 保存 delegate 原始响应 |
| `APPLESERVICES_DELEGATE_AUTH=mme\|mme-pet` | 换用 X-MobileMe-AuthToken 认证 |
| `APPLESERVICES_SOCKS_PROXY=host:port` | GSA 走 SOCKS5 |
| `APPLESERVICES_ADI_RINFO=<n>` | 覆盖 RINFO |
| `APPLESERVICES_ICLOUD_SETUP=<url>` | 覆盖 setup 域名（中国区用） |

### 6.3 诊断工具

| 工具 | 用途 |
|---|---|
| `tools/delegatedebug` | 带真实 anisette 头调 delegate，打印**原始响应**（当前指向 prefpane） |
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
APPLESERVICES_DELEGATE_PATH=prefpane \
APPLESERVICES_REMOTE_ANISETTE=1 \
APPLESERVICES_DELEGATE_DUMP=/tmp/dump.plist \
APPLESERVICES_APPLEID='<账号>' APPLESERVICES_PASSWORD='<密码>' \
./bin/trusted-device-gui.exe -native-adi -background -control-api -gui-auth \
  -twofactor sms -dir /tmp/idN \
  -adi-dll ./.cache/openbubbles-store/rust_lib_bluebubbles.dll -python python

# 3. 提交验证码（token 在 <dir>/control.json）
curl -X POST "http://127.0.0.1:<port>/action" \
  -H "Content-Type: application/json" \
  -H "X-Control-Token: <token>" \
  -d '{"action":"submit-sms","code":"<6位>"}'
```

---

## 7. 已知的坑

1. **`ani.sidestore.io` 已坏** —— 用 `ani.sidestore.app`。
2. **423 Locked** —— 同账号短时间内反复请求验证码会被限。换账号或等待。`Xcode` 客户端头会加剧（已移除）。
3. **PET 是一次性的** —— 每次 delegate 调用消耗一个 PET。用旧 session 重测会得到误导性的 `"password entered incorrectly"`。
4. **验证码必须及时提交** —— worker 重启会重置 challenge。
5. **`-native-adi` 会绕过远程 anisette** —— 测 provision 修复时必须加 `APPLESERVICES_REMOTE_ANISETTE=1`，否则 `adi_pb` 根本不被使用。
6. **`prefpane` 端点需要 `protocolVersion: 2`** —— 当前实现直接复用 iosbuddy 的 body，返回 400 但**仍带完整 ErrorID**，足以诊断。若要让它真正成功，需要按 §4 补上 `X-Mme-Nas-Qualify`。
7. **`id-validation-cert` 走 HTTP** —— HTTPS 版本不可达。

---

## 8. 建议的下一步

**按成功率排序：**

1. **【最可靠】在真机 iPhone/Mac 上登录一次该账号** —— 苹果一旦认识这个设备，`ICLOUD_INVALID_DEVICE_TOKEN` 就消失。之后 Windows 侧只需维持会话。
2. **实现 `X-Mme-Nas-Qualify`** —— 需要逆向 NAC 协议：
   - 从 `id-validation-cert` 取证书链
   - 用 `HardwareConfig` 构造 `SessionInfoRequest`
   - POST 到 `id-initialize-validation` 做密钥协商
   - 签名得到 `validation_data`，base64 后作为头
   - **参考**：`open-absinthe/src/nac.rs` 的 `HardwareConfig` 结构（说明需要哪些字段），但 `ValidationCtx` 的三个方法都是 `todo!()`
3. **抓包工作 DLL** —— 用 mitmproxy 拦截 `rust_lib_bluebubbles.dll` 的 delegate 请求，看 `X-Mme-Nas-Qualify` 的实际值（判断它是真签名还是固定值）。需要装证书，较侵入。
4. **确认 10-05 成功是否可复现** —— 检查 `sabrashrager4423` 当时的会话（已归档到 `archive/sold-sabrashrager4423-20261008/`），看它的 `service-data` 是否真的是 delegate 换来的。**注意：该账号已售出，不要用它做任何请求。**

---

## 9. 给下一个 AI 的提醒

- **不要再猜请求格式** —— §3 表格里 16 项全部排除过，附了实验方法。
- **诊断一律用 `APPLESERVICES_DELEGATE_PATH=prefpane`** —— 它给真实原因，`iosbuddy` 不给。
- **每次测试用全新账号或等冷却** —— `423` 和 PET 一次性会污染结果。
- **`-native-adi` 和远程 anisette 是两条路** —— 测 anisette 相关改动时必须明确用哪个。
- **本项目的测试全绿**（`go test ./...`），改动前先跑一遍。
