# Windows 本机验证码与设备登记

验证日期：2026-10-05。

## 真实结果

**Windows 本机生成的六位码已被 Apple 接受，另一个需要双重验证的新会话完成了 iCloud 登录。**

验收顺序：

1. 从本机 iCloud for Windows 的 APS COM 服务取得真实 32 字节推送令牌。
2. 为原有稳定身份生成本机 ADI 认证头，首次认证使用绑定手机号的短信验证码。
3. 完成账号认证、iCloud 令牌交换和 Apple 设备登记。
4. 重启程序，沿用本机 provisioning 状态重新认证，未重新要求短信。
5. 独立登录身份进入双重验证，请求受信任设备验证码；Windows 收到命令 100。
6. 请求随后收到命令 400（取消），没有声称 `allow` 按钮流程已通过。
7. 在 Windows 输入 `code`，由本机模块生成六位码，直接提交给待验证的新会话。
8. `SubmitCode(TrustedDevice, ...)` 成功，随后 `Client()` 成功，退出码 0。

测试期间发到 iPhone 的验证码没有用于第 7、8 步。文档记录验收结果，不附生成码或令牌。

用户随后提供官网截图，明确显示 `appleservices` 是“受信任设备”，可接收 Apple 账户验证码。
恢复登录后的真实设备列表查询返回 4 个条目，当前安装可见。

## Windows 启动

此机器已有完整的已签名 Store 包缓存与依赖：

```powershell
.\setup-native-adi.ps1
.\run-trusted-device.ps1 -Check
.\run-trusted-device.ps1
```

脚本提示账号密码，密码不回显。首次双重验证默认向绑定手机号发短信。
登录后可输入：

| 输入 | 行为 |
| --- | --- |
| `code` | 主动生成本机六位码，不批准任何推送请求 |
| `allow` | 允许当前未取消的请求，并显示本机验证码 |
| `deny` | 拒绝当前未取消的请求 |
| `devices` | 查询设备数量和本安装是否可见，不输出其他设备标识 |
| Ctrl+C | 退出监听 |

`code` 不需要存在未处理的通知；`allow` 会检查请求是否已经取消。
新登录的受信任设备测试可能同时通知账号原有的 iPhone，与首次短信验证使用不同的接口。

直接调用 CLI：

```powershell
go build -o .\bin\trusted-device.exe ./cmd/trusted-device
.\bin\trusted-device.exe -native-adi -twofactor sms
```

直接调用时凭据通过 `APPLESERVICES_APPLEID` / `APPLESERVICES_PASSWORD` 提供。
支持 `-dir`、`-adi-dll`、`-python`、`-name` 和 `-listen-for`。
`-native-adi` 自动启用原生推送，不能同时传外部生成器或推送证书。
`-check` 检查本地状态、模块哈希、Python 位数/版本与编译器，不访问 Apple。
沿用已有 `state/device.json`；其他机器先通过普通登录建立状态。

### 持续在线

```powershell
.\run-trusted-device.ps1 -Background
.\run-trusted-device.ps1 -Status
.\run-trusted-device.ps1 -Stop
```

后台通过隐藏的独立进程运行；关闭终端输入不会停止会话，不设置自动退出时长。
前台的输入关闭后也会保留登录和监听，只停止接收手动命令。
每 10 分钟使用同一身份和保存的 Cookie 重新验证账号，完成 iCloud 令牌交换，
续报设备并查询设备列表，随后保存更新 Cookie。
只有完整认证、账号匹配、登记和列表查询都成功才采用新的内存登录上下文。
刷新遇到临时错误时保留原有连接并记录 `refresh_failed`；
Apple 要求重新双重验证时记录 `verification_required`，退出并提示前台完成短信验证，
后台不自动反复发送短信。

`state/runtime.json` 保存 PID、在线/认证/推送状态、上次刷新时间、设备数量和安装可见性，
不含账号、凭据或设备标识。Cookie 更新通过临时文件替换，避免中途写入造成截断。
会话快照同时覆盖账号设备查询和登记接口的 Cookie。
`-Stop` 提交与当前 PID 匹配的本地停止请求，进程清理桥接后退出，保留登录状态文件。
默认不设置开机自启动，Windows 重启/注销后需重新运行脚本。

本项目的状态文件与官方 Windows iCloud 客户端认证状态独立。
官网受信任标记与项目在线已确认，不代表官方客户端的“验证你的 Apple 账户”提示已解决。

## 依赖和模块校验

- Windows x64、Go 1.26+、64 位 Python 3.10+、gcc/MinGW。
- 已安装的 iCloud for Windows；本机实测版本为 15.8.118.0。
- OpenBubbles Store 包 `OpenBubbles.OpenBubbles`，**1.18.800.0 x64**。
  Microsoft Store 产品 ID：`9PJMSNSQD0FV`。

项目不分发第三方 DLL，也不自动下载或安装 OpenBubbles。
其他包路径可用 `setup-native-adi.ps1 -PackagePath 'C:\path\package.msix'`。
脚本检查完整 MSIX 签名有效、发布者一致、包清单名称/版本/架构一致，
仅提取唯一的 `rust_lib_bluebubbles.dll` 并校验固定 SHA256：

```text
ca88e816dda449d7ba24d54a5ed709a2d392daeae28c25a61dcd2e7496e44cfe
```

本机包签名实测为 Valid。Go 启动前、Python 加载前都再次校验模块哈希；
原生绑定内容 hash 必须为 `-208244658`。
公共绑定对应源码版本 `dc276004ac4ee0bd8e7d7e2089da7b94695166c9`。
其他版本不能直接替换，需要重新核对 ABI 和真实验证码验收。

## 实现和状态

`nativeadi.Client` 同时提供 anisette 认证头和六位码，两者绑定同一个设备标识。
Python 通过已核对版本的 Flutter Rust Bridge SSE 接口调用实际 provisioning、headers 和
`get_2fa_code`，处理同步缓冲区、异步回调释放与 Rust 句柄所有权。
没有把 provisioning 状态检查返回值格式化成验证码。

原生模块的 factory 需要 APS 上下文。项目的独立 Python 进程通过 Winsock IAT shim
拒绝该模块闲置的 TCP 5223 连接，避免进入额外推送激活；真正的推送由 Apple COM 服务提供。
该 shim 只作用于当前辅助进程的这个模块，不修改系统防火墙或其他进程，
不跳过账号认证、许可验证或密码学检查。桥接代码每次在临时目录编译，退出清理。

原生 provisioning 会直接打印敏感诊断，加载 DLL 前关闭其 stdout/stderr，
保留单独 JSON IPC 输出。每个回复校验请求序号、设备身份和验证码格式。
账号密码不保存，也不继承给 Python、编译器或 APS 子进程。

APS 支持真实令牌、IDMS 主题、命令 100/400/1200、通知确认和账号过滤。
原生模式不导出 Apple 推送证书或私钥。
登记使用带推送令牌的 SRP CPD、heartbeat token 和 `postdata`；
`allow`/`deny` 通过 `teardown` 发送服务器动作。命令 700 的 Circle/PAKE 尚未实现，收到明确报错。

敏感本机状态：

- `state/device.json`：稳定标识、序列号、原远端 provisioning；本机路径不使用原远端 blob。
- `state/session.json`：绑定账号的可信 Cookie。
- `state/native-adi/hw_info.plist`：本机原生身份与推送状态。
- `state/native-adi/anisette_test/state.plist`：本机 ADI provisioning。
- `state/runtime.json`：不含敏感账号数据的当前运行状态。

保留整个状态目录以便重启复用，不保证迁移到其他机器可用。
默认 `state/`、`.cache/`、`bin/` 和 Python 字节码已加入 Git 忽略。
没有恢复钥匙串、读取照片/联系人/iMessage 或删除账号原有设备。

## 验收边界

- 用户提供的官网截图已明确显示受信任标记；恢复登录后项目查询到 4 个设备，本安装可见。
- 真实推送到达 Windows、本机生成码被新登录接受、iCloud 令牌交换均已通过。
  本次通过手动 `code`，推送的 `allow` 按钮完整流程还没有验收。
- Windows 仍依赖 Apple APS 服务及固定第三方原生模块，没有独立实现开源验证码算法。
- 本机 iCloud 的 `CoreADI64.dll` 六位码接口仍返回 `-45019`；新路径使用另一个实际有效的模块。
- ClearADI 公共 stub 的函数是占位代码，不能单靠它生成验证码。
- 尚未验证其他账号或所有 Apple 网页/服务验证流程。

`go test ./...`、`go vet ./...`、签名/哈希检查和 CLI 本地检查已通过。
新增测试覆盖回复身份和请求绑定、错误脱敏、子进程凭据过滤、生成码格式、错误模块拒绝。
保活测试覆盖未验证或账号不匹配的刷新拒绝、列表获取失败时不采用刷新结果、Cookie 快照恢复和本地停止的 PID 绑定。
真实后台验证使用 1 分钟间隔，完成一轮自动认证、登记、设备查询与 Cookie 保存；
随后正常停止并按默认 10 分钟间隔重启，继续查询到 4 个设备，本安装可见。
本地测试本身不代替上述真实账号验收。

## 依据

- [OpenBubbles 公共绑定对应版本](https://github.com/OpenBubbles/openbubbles-app/tree/dc276004ac4ee0bd8e7d7e2089da7b94695166c9/rust)。
- [RustPush AuthKit](https://github.com/OpenBubbles/rustpush/blob/master/src/auth.rs)。
- [登记与 teardown](https://github.com/OpenBubbles/apple-private-apis/blob/master/icloud-auth/src/client.rs)。
- [ClearADI 公共占位实现](https://github.com/OpenBubbles/clearadi-stub/blob/main/src/lib.rs)。
- [原 CoreADI 六位码调用](https://github.com/Dadoum/apple-crates/blob/master/adi/src/calls.rs)。
- Windows COM 接口来自本机 iCloud 包类型库与清单；原生模块来自已验证签名的完整 Store 包。
