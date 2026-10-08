# 账号服务条款更新：已完成并验证

## Apple 当前返回的页面

- 对象：用户本轮提供的账号，对应 `state/accounts/02-account`。
- 操作模式：existing-account update；设备合格标记为 true。
- 条款页地区代码：JP。这是该页面的返回值，不据此改动账号地区。
- iCloud 条款版本：634501。
- Apple 媒体服务条款版本：290462。

条款正文参考：[iCloud 日本地区条款](https://www.apple.com/legal/internet-services/icloud/jp/terms.html)、[Apple 媒体服务日本地区条款](https://www.apple.com/legal/internet-services/itunes/jp/terms.html)。本轮 Apple 返回页面的完整正文仅保存在本地 `.cache/activation-02-account-setup.html`；该文件还含认证材料，不应发布或作为普通网页打开。

## 准备提交的请求

```text
POST https://setup.icloud.com/setup/web/basic_auth_update_account/{account}/{native-udid}
Content-Type: application/x-www-form-urlencoded; charset=UTF-8
Authorization: Basic <当前账号的 setup 凭据>
```

提交页面给出的两项条款版本及本账号已有的设备/APS 参数。请求使用同一个 setup cookie jar。不携带营销邮件、姓名、生日、密码、账号地区或购买参数。

执行入口为 `accountsetupcheck -accept-terms -expected-terms JP:634501:290462`。用户已明确同意，Apple 返回 `HTTP 200`、`success=true` 和接受条款响应头。没有再次提交。

提交只发送一次。以 Apple 的明确 `success=true` 响应确认更新，再交换 iCloud delegate 并读取配额。请求超时或未得到明确回应时保留“结果未知”，不自动重试。

条款写入本身已确认；随后独立的 Mac v2/NAC 流程也取得 MME 令牌并读到配额。两者是可分别验证的步骤。
