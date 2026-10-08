"""Read Windows CoreADI machine anisette without launching iCloud UI.

ABI reference: Dadoum/apple-crates, adi/src/{calls,core_adi}.rs.
Only initialize, production-machine status/routing, OTP and buffer release are
used. This helper does not provision, erase, list user accounts or submit login.
Default output contains statuses and lengths only. --headers-out explicitly
writes private MD/MID/RINFO values for a caller; it is not a full device profile.
"""
import argparse
import base64
import ctypes
import json
import os
from pathlib import Path
import struct
import sys


class Parameters(ctypes.Structure):
    _fields_ = [("payload", ctypes.c_void_p), ("input_size", ctypes.c_uint32),
                ("output_size", ctypes.c_uint32), ("flags", ctypes.c_uint32)]


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dll", type=Path)
    parser.add_argument("--headers-out", type=Path)
    parser.add_argument("--headers-pipe", action="store_true", help="private parent-process protocol; never use in a terminal")
    args = parser.parse_args()
    if sys.platform != "win32" or ctypes.sizeof(ctypes.c_void_p) != 8:
        raise SystemExit("Requires 64-bit Windows Python")
    if not args.dll.is_absolute() or not args.dll.is_file():
        raise SystemExit("Supply an absolute installed CoreADI64.dll path")
    if args.headers_out and args.headers_out.exists():
        raise SystemExit("Private output already exists; choose a new path")
    if args.headers_pipe and (sys.stdout.isatty() or args.headers_out):
        raise SystemExit("Private pipe mode requires redirected stdout and no output file")
    search = os.add_dll_directory(str(args.dll.parent))
    try:
        library = ctypes.WinDLL(str(args.dll))
        dispatch = library.vdfut768ig
    except OSError as failure:
        raise SystemExit("CoreADI load failed; Windows error=" + str(failure.winerror))
    except AttributeError:
        raise SystemExit("Installed CoreADI dispatcher unavailable")
    dispatch.argtypes = [ctypes.c_uint32, ctypes.POINTER(Parameters)]
    dispatch.restype = ctypes.c_int32

    def call(magic, payload=b""):
        data = struct.pack(">II", 1, 1) + payload
        buffer = ctypes.create_string_buffer(data)
        parameters = Parameters(ctypes.addressof(buffer), len(data), 4, 0)
        status = dispatch(magic, ctypes.byref(parameters))
        return status if status < 0 else struct.unpack(">i", buffer.raw[:4])[0]

    def ptr(obj):
        return ctypes.addressof(obj)

    init = call(0x12DB31C5)
    version = sys.getwindowsversion()
    facts = {"initialize-status": init, "scope": "production machine -2",
             "windows-version": f"{version.major}.{version.minor}.{version.build}"}
    if init < 0:
        print(json.dumps(facts))
        return 1
    try:
        provisioned = call(0xB0EDA7AF, struct.pack(">q", -2))
        facts["machine-status"] = provisioned
        # Source maps 0 to provisioned, -45061 to not provisioned.
        facts["machine-provisioned"] = provisioned == 0
        if provisioned != 0:
            print(json.dumps(facts))
            return 1
        routing = ctypes.c_uint64()
        routing_status = call(0x85FE63B0, struct.pack(">qQ", -2, ptr(routing)))
        facts["routing-status"] = routing_status
        mid, otp = ctypes.c_void_p(), ctypes.c_void_p()
        mid_len, otp_len = ctypes.c_uint32(), ctypes.c_uint32()
        status = call(0xCFE0B46A, struct.pack(">qQQQQ", -2, ptr(mid), ptr(mid_len), ptr(otp), ptr(otp_len)))
        facts.update({"otp-status": status, "mid-bytes": mid_len.value, "otp-bytes": otp_len.value})
        if status < 0:
            print(json.dumps(facts))
            return 1
        try:
            if not mid.value or not otp.value or not 1 <= mid_len.value <= 65536 or not 1 <= otp_len.value <= 65536:
                raise SystemExit("CoreADI returned invalid output buffers")
            if args.headers_out or args.headers_pipe:
                if routing_status < 0:
                    raise SystemExit("No valid routing; private headers were not written")
                headers = {"X-Apple-I-MD-M": base64.b64encode(ctypes.string_at(mid, mid_len.value)).decode(),
                           "X-Apple-I-MD": base64.b64encode(ctypes.string_at(otp, otp_len.value)).decode(),
                           "X-Apple-I-MD-RINFO": str(routing.value)}
                if args.headers_out:
                    with args.headers_out.open("x", encoding="utf-8") as output:
                        json.dump(headers, output)
                    facts["private-headers-written"] = True
            facts["apple-login-verified"] = False
            print(json.dumps({"headers": headers, "facts": facts}) if args.headers_pipe else json.dumps(facts))
            return 0
        finally:
            for buffer_ptr in (mid, otp):
                if buffer_ptr.value:
                    call(0x3E58E7F9, struct.pack(">Q", buffer_ptr.value))
    finally:
        call(0x08C0FE9E)
        search.close()


if __name__ == "__main__":
    raise SystemExit(main())
