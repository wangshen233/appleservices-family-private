param([string]$Path = '')
$ErrorActionPreference = 'Stop'
if (-not $Path) {
    $package = Get-AppxPackage AppleInc.iCloud | Select-Object -First 1
    if (-not $package) { throw 'Official iCloud for Windows is not installed for this user.' }
    $Path = Join-Path $package.InstallLocation 'iCloud\iCloudHome.exe'
}
if (-not ('ICloudTypeLibrary' -as [type])) {
    Add-Type -TypeDefinition @'
using System;
using System.Runtime.InteropServices;
using System.Runtime.InteropServices.ComTypes;
using TYPEDESC = System.Runtime.InteropServices.ComTypes.TYPEDESC;
using TYPEATTR = System.Runtime.InteropServices.ComTypes.TYPEATTR;
using FUNCDESC = System.Runtime.InteropServices.ComTypes.FUNCDESC;
using ELEMDESC = System.Runtime.InteropServices.ComTypes.ELEMDESC;
public static class ICloudTypeLibrary {
    [DllImport("oleaut32.dll", CharSet=CharSet.Unicode, PreserveSig=false)]
    static extern void LoadTypeLibEx(string path, int registration, out ITypeLib library);
    static string TypeName(ITypeInfo info, TYPEDESC desc) {
        if (desc.vt == 26) {
            return TypeName(info, (TYPEDESC)Marshal.PtrToStructure(desc.lpValue, typeof(TYPEDESC))) + "*";
        }
        if (desc.vt == 29) {
            ITypeInfo referenced; info.GetRefTypeInfo((int)desc.lpValue, out referenced);
            string name, doc, help; int context;
            referenced.GetDocumentation(-1, out name, out doc, out context, out help);
            Marshal.ReleaseComObject(referenced); return name;
        }
        return ((VarEnum)desc.vt).ToString();
    }
    public static void Inspect(string path) {
        ITypeLib library; LoadTypeLibEx(path, 2, out library);
        try {
            for (int i=0; i<library.GetTypeInfoCount(); ++i) {
                ITypeInfo info; library.GetTypeInfo(i, out info);
                IntPtr attrPointer; info.GetTypeAttr(out attrPointer);
                try {
                    TYPEATTR attr=(TYPEATTR)Marshal.PtrToStructure(attrPointer,typeof(TYPEATTR));
                    string name,doc,help; int context;
                    info.GetDocumentation(-1,out name,out doc,out context,out help);
                    if (name!="INCAccount" && name!="NCAccount" && name!="IAccountInfo" && name!="AccountInfo") continue;
                    Console.WriteLine(name+" "+attr.typekind+" "+attr.guid);
                    for(int j=0; j<attr.cFuncs; ++j) {
                        IntPtr functionPointer; info.GetFuncDesc(j,out functionPointer);
                        try {
                            FUNCDESC function=(FUNCDESC)Marshal.PtrToStructure(functionPointer,typeof(FUNCDESC));
                            string[] names=new string[function.cParams+1]; int count;
                            info.GetNames(function.memid,names,names.Length,out count);
                            string line="  slot="+(function.oVft/IntPtr.Size)+" "+TypeName(info,function.elemdescFunc.tdesc)+" "+names[0]+"(";
                            for(int p=0; p<function.cParams; ++p) {
                                ELEMDESC arg=(ELEMDESC)Marshal.PtrToStructure(IntPtr.Add(function.lprgelemdescParam,p*Marshal.SizeOf(typeof(ELEMDESC))),typeof(ELEMDESC));
                                line+=(p==0?"":", ")+TypeName(info,arg.tdesc)+" "+(p+1<count?names[p+1]:"arg"+p)+" "+arg.desc.paramdesc.wParamFlags;
                            }
                            Console.WriteLine(line+")");
                        } finally {info.ReleaseFuncDesc(functionPointer);}
                    }
                } finally { info.ReleaseTypeAttr(attrPointer); Marshal.ReleaseComObject(info); }
            }
        } finally {Marshal.ReleaseComObject(library);}
    }
}
'@
}
[ICloudTypeLibrary]::Inspect($Path)
