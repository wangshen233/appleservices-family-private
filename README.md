# appleservices — iCloud 登录版

这个版本只保留设备身份认证、Apple ID 登录、双重验证和 iCloud 登录令牌获取。
成功获取 `mmeAuthToken` 后结束，不恢复钥匙串，不读取密码、照片、联系人或设备列表，也不修改云端数据。

## Windows 运行

需要 Go 1.26 或更新版本（已有 `bin/login.exe` 时可直接运行）。

```powershell
.\run-login.ps1
```

脚本会提示输入 Apple ID 和密码，密码输入不回显。首次登录可能需要验证码。
等待验证码时可输入 `resend` 重发当前方式，输入 `sms` 改为短信，输入 `device` 改为受信任设备验证码。
默认向受信任设备请求验证码，也可以选短信：

```powershell
.\run-login.ps1 -TwoFactor sms
```

使用自己的 anisette 服务：

```powershell
.\run-login.ps1 -Anisette 'https://your-anisette-server.example'
```

程序默认使用公共 anisette 服务池。设备标识及 provisioning 数据会发送给选用的 anisette 服务；
账号密码和验证码由本程序直接提交 Apple 的登录接口。

## 命令行

```powershell
go build -o .\bin\login.exe ./cmd/login
.\bin\login.exe -help
```

可用参数：

| 参数 | 用途 |
| --- | --- |
| `-appleid` | Apple ID，也可用 `APPLESERVICES_APPLEID` |
| `-password` | 账号密码，推荐通过脚本隐藏输入或 `APPLESERVICES_PASSWORD` 提供 |
| `-dir` | 状态目录，默认 `./state`，也可用 `APPLESERVICES_DIR` |
| `-twofactor` | `trusted-device` 或 `sms` |
| `-anisette` | 自定义 anisette 服务地址 |
| `-logout` | 删除本地会话，不会撤销 Apple 服务器上的会话 |

成功时只输出：

```text
iCloud login successful. Trusted session saved.
```

失败返回非零退出码。程序不打印密码、令牌、Cookie 或账号内部编号。

## 库调用

```go
login, err := appleservices.BeginLogin(
    appleservices.Credentials{AppleID: appleID, Password: password},
    store, // 实现 appleservices.Store；文件实现见 cmd/login/filestore.go
)
if err != nil {
    return err
}
if login.NeedsTwoFactor() {
    if err := login.RequestCode(appleservices.TrustedDevice); err != nil {
        return err
    }
    if err := login.SubmitCode(appleservices.TrustedDevice, code); err != nil {
        return err
    }
}
client, err := login.Client() // 完成 iCloud 登录，保存可信会话
if err != nil {
    return err
}
// client.DSID、client.ADSID、client.MMEAuthToken、client.CloudKitToken
// 是敏感的登录结果，按需在内存使用，不要打印到日志。
_ = client
```

`BeginLogin` 每次都会向 Apple 验证账号密码，缓存会话仅用于复用可信 Cookie、减少验证码提示。
必须成功调用 `Client()` 才算完成 iCloud 登录。
账号不匹配或缺少账号绑定信息的旧会话会被忽略；失效会话可能重新触发双重验证。

## 保存的状态

- `state/device.json`：稳定设备标识和 anisette provisioning 数据。
- `state/session.json`：账号绑定信息和可信 Cookie，不保存账号密码、验证码、设备锁屏密码或 iCloud 令牌。

状态目录含敏感认证数据，请保存在当前用户可访问的目录，不要上传到 Git。
默认的 `state/` 已加入 `.gitignore`；使用自定义目录时自行排除。

## 登录流程与修复

1. 获取 anisette 设备认证头。
2. GrandSlam / SRP 账号认证。
3. 如果需要，提交受信任设备或短信验证码，然后重新认证。
4. 从认证结果提取 PET，调用 iCloud `loginDelegates` 获取登录令牌。
5. 保存可信 Cookie 并结束。

已移除 `cloudkit`、`ckks`、`escrow`、`octagon`、`keychain` 以及原密码导出命令。
修复缓存存在时提前跳过认证、无需验证码时不保存会话的问题；会话现在绑定账号。
同时保留 anisette 服务提供的 User-Agent，并在刷新认证头失败时返回错误，避免使用过期数据。

2026-10-05 又对照上游进行了协议核查：补上 9 月旧 Xcode 客户端标识的兼容处理，
区分 HTTP 拒绝与 Apple 认证错误，兼容短信 JSON/HTML 返回格式，校验验证码响应中的 plist 错误。
`s2k`、`s2k_fo` 的计算已通过独立 Python SRP 实现生成的参考向量验证。
来源、无凭据探测结果及验证边界见 [协议核查记录](docs/protocol-audit.md)。

Apple 请求的证书验证传输现在遵循 `HTTP_PROXY` / `HTTPS_PROXY` / `NO_PROXY` 配置。
原来的传输会绕过环境代理，可能在设备初始化之前就出现连接被拒绝。

GSA 认证请求现在各用一条独立 HTTP/1 连接，避免复用连接导致 `init` 成功、
`complete` 返回 429。Cookie、设备身份、代理和证书验证保持连续。
实际验证已通过原账号的密码认证和服务端证明校验，进入双重验证码阶段；
随后完成短信验证、iCloud 登录令牌交换及会话保存。完整记录见协议核查记录。

短信验证码使用 GSA `/auth/verify/phone` 接口；支持自动发送短信的 201 返回。
验证码校验响应中的 PET 和服务令牌会保留在内存中，直接用于完成登录，避免丢弃令牌后再次触发双重验证。

## 验证与限制

```powershell
go test ./...
go vet ./...
```

测试使用本地模拟认证和本地 HTTP 服务，不访问真实 Apple 账号。
本次已使用用户授权账号完成真实短信验证和 iCloud 登录；未额外验证其他账号或后续免验证码登录。
这是非官方私有协议实现。

原始源码保存在 Git 的初始提交 `d6fb108`，也有 `backups/original-source.zip` 备份。

MIT License；与 Apple 无关联。
