param(
    [string]$AccountsFile = (Join-Path $PSScriptRoot 'accounts.json'),
    [string]$StateRoot = (Join-Path $PSScriptRoot 'accounts'),
    [ValidateRange(1024,65535)][int]$BasePort = 18741,
    [string]$ADILibrary = (Join-Path $PSScriptRoot '.cache\openbubbles-store\rust_lib_bluebubbles.dll'),
    [string]$Python = 'python',
    [switch]$NoOpen
)

$ErrorActionPreference = 'Stop'
$accounts = @(Get-Content -LiteralPath $AccountsFile -Raw | ConvertFrom-Json)
if ($accounts.Count -eq 0) { throw 'Accounts file is empty.' }
$worker = Join-Path $PSScriptRoot 'bin\trusted-device-gui.exe'
$dashboard = Join-Path $PSScriptRoot 'bin\dashboard.exe'
if (-not (Test-Path -LiteralPath $worker) -or -not (Test-Path -LiteralPath $dashboard)) {
    throw 'Build the project first so bin\trusted-device-gui.exe and bin\dashboard.exe exist.'
}
New-Item -ItemType Directory -Force -Path $StateRoot | Out-Null
$started = @()
$hubRoot = Join-Path $StateRoot '_hub'
New-Item -ItemType Directory -Force -Path $hubRoot | Out-Null
$hubAccounts = [System.Text.StringBuilder]::new()
for ($index = 0; $index -lt $accounts.Count; $index++) {
    $account = $accounts[$index]
    if ([string]::IsNullOrWhiteSpace($account.name) -or [string]::IsNullOrWhiteSpace($account.apple_id) -or [string]::IsNullOrEmpty($account.password)) {
        throw "Account entry $($index + 1) must contain name, apple_id and password."
    }
    $safe = ($account.name -replace '[^a-zA-Z0-9._-]', '_').Trim('_')
    if ([string]::IsNullOrWhiteSpace($safe)) { $safe = "account-$($index + 1)" }
    $dir = Join-Path $StateRoot ("{0:D2}-{1}" -f ($index + 1), $safe)
    $port = $BasePort + $index + 1
    New-Item -ItemType Directory -Force -Path $dir | Out-Null
    if (-not (Test-Path -LiteralPath (Join-Path $dir 'device.json'))) {
        throw "Account '$($account.name)' has no provisioned device state in $dir. Complete its first login into that account directory before starting multi-account mode."
    }
    $url = "http://127.0.0.1:$port"
    try { $existing = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 1 } catch { $existing = $null }
    if ($existing) {
        if ($existing.Headers['X-AppleServices-UI'] -ne '1') { throw "Port $port is already in use." }
        $started += [pscustomobject]@{ Name = $account.name; Port = $port; URL = $url; PID = 'existing' }
        continue
    }
    $info = [Diagnostics.ProcessStartInfo]::new()
    $info.FileName = $dashboard
    $info.WorkingDirectory = $PSScriptRoot
    $info.UseShellExecute = $false
    $info.CreateNoWindow = $true
    $info.Arguments = "-dir `"$dir`" -worker `"$worker`" -adi-dll `"$ADILibrary`" -python `"$Python`" -port $port"
    $info.EnvironmentVariables['APPLESERVICES_APPLEID'] = [string]$account.apple_id
    $info.EnvironmentVariables['APPLESERVICES_PASSWORD'] = [string]$account.password
    $info.RedirectStandardOutput = $true
    $info.RedirectStandardError = $true
    $process = [Diagnostics.Process]::new()
    $process.StartInfo = $info
    if (-not $process.Start()) { throw "Could not start dashboard for $($account.name)." }
    $started += [pscustomobject]@{ Name = $account.name; Port = $port; URL = $url; PID = $process.Id }
    $deadline = [DateTime]::UtcNow.AddSeconds(10)
    while ([DateTime]::UtcNow -lt $deadline) {
        try { $ready = Invoke-WebRequest -Uri $url -UseBasicParsing -TimeoutSec 1 } catch { $ready = $null }
        if ($ready -and $ready.Headers['X-AppleServices-UI'] -eq '1') { break }
        Start-Sleep -Milliseconds 200
    }
    if (-not $ready) { throw "Dashboard for $($account.name) did not start; inspect $dir\dashboard.err.log." }
    $displayName = [System.Net.WebUtility]::HtmlEncode([string]$account.name)
    $avatar = ([string]$account.name).Substring(0,1).ToUpper()
    $accountButton = '<button class="account-item" data-name="' + $displayName + '" data-url="' + $url + '"><span class="account-avatar">' + $avatar + '</span><span><strong>' + $displayName + '</strong><small>' + $url + '</small></span><i></i></button>'
    [void]$hubAccounts.AppendLine($accountButton)
}
$hub = @"
<!doctype html><html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>Apple 多账号控制台</title>
<style>
:root{font-family:"Segoe UI","Microsoft YaHei",system-ui,sans-serif;color:#203249;background:#f4f7fb}*{box-sizing:border-box}body{margin:0;min-height:100vh;display:flex}.sidebar{width:280px;background:#172f4a;color:#dce8f5;padding:28px 18px;display:flex;flex-direction:column}.brand{display:flex;align-items:center;gap:11px;font-size:17px;font-weight:700;padding:0 10px 30px}.brand-mark{width:38px;height:38px;border-radius:12px;background:#315a82;display:grid;place-items:center;color:#8ce2bd;font:26px Georgia}.eyebrow{font-size:10px;letter-spacing:1.5px;color:#8ea6be;margin:8px 12px 12px}.account-list{display:grid;gap:8px}.account-item{border:1px solid transparent;background:transparent;color:inherit;border-radius:11px;padding:11px 10px;display:flex;align-items:center;gap:10px;text-align:left;cursor:pointer}.account-item:hover{background:#ffffff0c}.account-item.selected{background:#ffffff14;border-color:#ffffff18}.account-item strong,.account-item small{display:block;white-space:nowrap;overflow:hidden;text-overflow:ellipsis;max-width:150px}.account-item strong{font-size:12px}.account-item small{font-size:10px;color:#8fa8c1;margin-top:4px}.account-item i{margin-left:auto;width:7px;height:7px;border-radius:50%;background:#59d09b;box-shadow:0 0 0 3px #59d09b18}.account-avatar{width:31px;height:31px;border-radius:10px;background:#315a82;display:grid;place-items:center;color:#b9d3eb;font-weight:700}.sidebar-note{margin-top:auto;border-top:1px solid #ffffff15;padding:18px 10px 0;color:#8fa8c1;font-size:10px;line-height:1.7}.content{flex:1;padding:34px 42px;min-width:0}.topbar{display:flex;align-items:flex-start;justify-content:space-between;margin-bottom:24px}.kicker{font-size:10px;letter-spacing:1.7px;color:#8b9aae}.topbar h1{font-size:28px;letter-spacing:-.8px;margin:9px 0 7px}.topbar p{font-size:12px;color:#8c9bad;margin:0}.online{display:flex;align-items:center;gap:8px;background:#fff;border:1px solid #e5ebf2;border-radius:9px;padding:9px 12px;font-size:11px;color:#5e7188}.online i{width:7px;height:7px;border-radius:50%;background:#48bd89}.workspace{background:#fff;border:1px solid #e3eaf2;border-radius:16px;overflow:hidden;box-shadow:0 12px 34px #19395712}.workspace-head{display:flex;align-items:center;justify-content:space-between;padding:16px 18px;border-bottom:1px solid #edf1f6}.workspace-head strong{font-size:13px}.workspace-head a{color:#416fe0;font-size:11px;text-decoration:none}.workspace iframe{display:block;width:100%;height:calc(100vh - 180px);min-height:680px;border:0;background:#fff}@media(max-width:800px){.sidebar{width:220px}.content{padding:24px 18px}.topbar h1{font-size:23px}}
</style></head><body><aside class="sidebar"><div class="brand"><span class="brand-mark">a</span><span>appleservices<small style="display:block;font-size:9px;color:#8fa8c1;margin-top:3px">多账号控制台</small></span></div><div class="eyebrow">账号列表</div><nav class="account-list">$hubAccounts</nav><div class="sidebar-note">每个账号使用独立会话、设备身份和刷新任务。后台在线状态由各自面板实时维护。</div></aside><main class="content"><header class="topbar"><div><div class="kicker">APPLE WORKSPACE</div><h1 id="title">选择账号</h1><p id="subtitle">从左侧选择一个账号开始管理设备和登录状态。</p></div><div class="online"><i></i><span>后台服务运行中</span></div></header><section class="workspace"><div class="workspace-head"><strong id="workspaceTitle">账号工作区</strong><a id="openLink" target="_blank">新标签页打开 ↗</a></div><div class="workspace"><iframe id="accountFrame" title="账号工作区"></iframe></div></section></main><script>
const items=[...document.querySelectorAll('.account-item')],frame=document.getElementById('accountFrame'),title=document.getElementById('title'),subtitle=document.getElementById('subtitle'),workspaceTitle=document.getElementById('workspaceTitle'),openLink=document.getElementById('openLink');function select(button){const data=button.dataset;items.forEach(x=>x.classList.toggle('selected',x===button));const target=data.url+'/#overview';frame.src=target;openLink.href=target;title.textContent=data.name;subtitle.textContent='管理设备、登录状态和验证码';workspaceTitle.textContent=data.name+' · 账号工作区';localStorage.setItem('selected-account',data.name)}items.forEach(x=>x.addEventListener('click',()=>select(x)));const saved=localStorage.getItem('selected-account');select(items.find(x=>x.dataset.name===saved)||items[0]);
</script></body></html>
"@
Set-Content -LiteralPath (Join-Path $hubRoot 'index.html') -Value $hub -Encoding UTF8
$hubProcess = Start-Process -FilePath $Python -ArgumentList @('-m','http.server',[string]$BasePort,'--bind','127.0.0.1') -WorkingDirectory $hubRoot -WindowStyle Hidden -PassThru
Write-Host "Hub ready: http://127.0.0.1:$BasePort/ (PID $($hubProcess.Id))"
$started | Format-Table -AutoSize
if (-not $NoOpen) { Start-Process "http://127.0.0.1:$BasePort/" }
