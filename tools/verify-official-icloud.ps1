param(
    [ValidateSet('Refresh','Verify','Terms','Probe','Storage')][string]$Action = 'Refresh',
    [string]$SessionFile = '',
    [string]$Python = 'python'
)
$ErrorActionPreference = 'Stop'
# GUIDs and method order are read from the installed iCloud 15.8 type library.
# Use the official COM server; do not write its caches or import project tokens.
Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Net;
using System.Text;
using System.IO;
public static class OfficialICloudAccount {
    [ComImport, Guid("02b21c08-c2cd-4698-a72f-29f2a6403851"), InterfaceType(ComInterfaceType.InterfaceIsIUnknown)]
    interface INCAccount {
        void Initialize();
        void RefreshInfo(out int error);
        void CopyAuthInfo([MarshalAs(UnmanagedType.BStr)] string url, [MarshalAs(UnmanagedType.BStr)] string realm,
            [MarshalAs(UnmanagedType.BStr)] out string dsid, [MarshalAs(UnmanagedType.BStr)] out string token, out int error);
        void ShowAuthDialog(out int error);
        void ShowTOSDialog(out int error);
        void CacheNewAuthToken([MarshalAs(UnmanagedType.BStr)] string token, [MarshalAs(UnmanagedType.BStr)] string realm);
        void IsIcloudAccount([MarshalAs(UnmanagedType.BStr)] string users, [MarshalAs(UnmanagedType.BStr)] out string validUsers, out int error);
        void GetMailAuthInfo(short attempt, [MarshalAs(UnmanagedType.BStr)] out string dsid, [MarshalAs(UnmanagedType.BStr)] out string token, out int error);
    }
    public static void Run(string action, string storageUrl) {
        object accountObject=null; string stage="create-service";
        try {
            // AccountInfo exists in the legacy type library but is not registered
            // by the Store package. Use only the manifest-registered NCAccount.
            stage="initialize";
            accountObject=Activator.CreateInstance(Type.GetTypeFromCLSID(new Guid("c5b12896-dbb7-4419-85e0-9c6d6f51e64e")));
            INCAccount service=(INCAccount)accountObject;
            service.Initialize();
            int error;
            if(action=="Storage") {
                stage="storage-auth-info";Uri uri;
                if(!Uri.TryCreate(storageUrl,UriKind.Absolute,out uri)||uri.Scheme!="https"||uri.Port!=443||
                    !System.Text.RegularExpressions.Regex.IsMatch(uri.Host,@"^p[0-9]+-quota\.icloud\.com$")||
                    !uri.AbsolutePath.EndsWith("/getManageCloudStorage")||!String.IsNullOrEmpty(uri.Query)||!String.IsNullOrEmpty(uri.UserInfo)) {
                    Console.WriteLine("{\"ok\":false,\"stage\":\"storage-url-invalid\"}");return;
                }
                string dsid,token;
                service.CopyAuthInfo(storageUrl,"",out dsid,out token,out error);
                if(error!=0||String.IsNullOrEmpty(dsid)||String.IsNullOrEmpty(token)) {
                    Console.WriteLine("{\"ok\":false,\"stage\":\"storage-auth-info\",\"code\":"+error+"}");return;
                }
                if(!uri.AbsolutePath.Contains("/"+dsid+"/")) {
                    Console.WriteLine("{\"ok\":false,\"stage\":\"storage-account-mismatch\"}");return;
                }
                stage="storage-request";
                HttpWebRequest request=(HttpWebRequest)WebRequest.Create(uri);
                request.Method="GET";request.AllowAutoRedirect=false;request.Timeout=15000;
                request.Accept="application/json";
                request.Headers[HttpRequestHeader.Authorization]="Basic "+Convert.ToBase64String(Encoding.UTF8.GetBytes(dsid+":"+token));
                dsid=null;token=null;
                HttpWebResponse response;
                try {response=(HttpWebResponse)request.GetResponse();}
                catch(WebException ex) {response=ex.Response as HttpWebResponse;if(response==null)throw;}
                using(response) {
                    int status=(int)response.StatusCode;
                    // Inspect only a bounded response for the known terms error.
                    string body="";using(StreamReader reader=new StreamReader(response.GetResponseStream())) {
                        char[] buffer=new char[8192];int read=reader.ReadBlock(buffer,0,buffer.Length);body=new string(buffer,0,read);
                    }
                    bool terms=body.Contains("MOBILEME_TERMS_OF_SERVICE_UPDATE");
                    Console.WriteLine("{\"ok\":"+(status==200?"true":"false")+",\"stage\":\"storage-request\",\"http_status\":"+status+",\"terms_required\":"+(terms?"true":"false")+"}");
                }
                return;
            }
            if (action=="Probe") {
                stage="auth-info"; string dsid, token;
                service.CopyAuthInfo("https://setup.icloud.com", "", out dsid,out token,out error);
                bool available=!String.IsNullOrEmpty(dsid)&&!String.IsNullOrEmpty(token);
                dsid=null;token=null;
                Console.WriteLine("{\"ok\":"+(error==0&&available?"true":"false")+",\"stage\":\"auth-info\",\"code\":"+error+",\"credentials_available\":"+(available?"true":"false")+"}");
                return;
            }
            if (action=="Verify") {stage="verify";service.ShowAuthDialog(out error);}
            else if (action=="Terms") {stage="terms";service.ShowTOSDialog(out error);}
            else {stage="refresh";service.RefreshInfo(out error);}
            Console.WriteLine("{\"ok\":"+(error==0?"true":"false")+",\"stage\":\""+stage+"\",\"code\":"+error+"}");
        } catch (Exception ex) {
            Console.WriteLine("{\"ok\":false,\"stage\":\""+stage+"\",\"hresult\":\""+ex.HResult.ToString("X8")+"\"}");
        } finally {
            if(accountObject!=null)Marshal.ReleaseComObject(accountObject);
        }
    }
}
'@
$expectedAccount = ''
if ($SessionFile) {
    $session = Get-Content -LiteralPath $SessionFile -Raw | ConvertFrom-Json
    $expectedAccount = [string]$session.AppleID
    if (-not $expectedAccount) { throw 'The selected project session has no account identity.' }
    # Refuse to touch an official installation without the selected account cache.
    # This is a cache-presence guard, not proof of an authenticated account.
    $cache = Join-Path $env:LOCALAPPDATA 'Packages\AppleInc.iCloud_nzyj5cx40ttqa\LocalCache\Roaming\Apple Computer\Preferences\com.apple.AOSKit.plist'
    $accountID = [string]$session.DSID
    if (-not $accountID -or -not (Test-Path -LiteralPath $cache) -or
        -not [Text.Encoding]::UTF8.GetString([IO.File]::ReadAllBytes($cache)).Contains($accountID)) {
        Write-Output '{"ok":false,"stage":"account-cache-mismatch"}'
        exit 0
    }
}
$storageUrl = ''
if ($Action -eq 'Storage') {
    # Decode only the cached endpoint, never extract or print authentication data.
    $storageUrl = (& $Python -c "import pathlib,plistlib; p=pathlib.Path.home()/'AppData/Local/Packages/AppleInc.iCloud_nzyj5cx40ttqa/LocalCache/Roaming/Apple Computer/Preferences/iCloudWinPref.plist'; print(plistlib.loads(p.read_bytes())['StorageData']['entry_points']['quota.manage_storage_url'])")
    if ($LASTEXITCODE -ne 0) { throw 'The official cached storage endpoint is unavailable.' }
}
[OfficialICloudAccount]::Run($Action, $storageUrl)
