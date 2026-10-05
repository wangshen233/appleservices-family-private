"""Probe the installed Windows ADI dispatcher, without provisioning or secrets.

Pass the absolute path to a locally staged CoreADI64.dll from your own iCloud
installation. This is a capability diagnostic, not a six-digit code provider.
ABI reference: github.com/Dadoum/apple-crates, adi/src/{calls,core_adi}.rs.
"""
import ctypes
import pathlib
import struct
import sys


class Parameters(ctypes.Structure):
    _fields_ = [("payload", ctypes.c_void_p), ("input_size", ctypes.c_uint32),
                ("output_size", ctypes.c_uint32), ("flags", ctypes.c_uint32)]


def main():
    if sys.platform != "win32" or ctypes.sizeof(ctypes.c_void_p) != 8:
        raise SystemExit("This probe requires 64-bit Python on Windows.")
    if len(sys.argv) != 2:
        raise SystemExit("Usage: python tools/probe-native-adi.py C:\\path\\CoreADI64.dll")
    path = pathlib.Path(sys.argv[1])
    if not path.is_absolute() or not path.is_file():
        raise SystemExit("An absolute path to the locally installed/staged DLL is required.")
    try:
        library = ctypes.WinDLL(str(path))
    except OSError as failure:
        raise SystemExit("CoreADI loading failed; Windows error=" + str(failure.winerror))
    dispatch = library.vdfut768ig
    dispatch.argtypes = [ctypes.c_uint32, ctypes.POINTER(Parameters)]
    dispatch.restype = ctypes.c_int32

    def call(magic, arguments=b""):
        # Clear payload, marker 1, then fixed-width big-endian arguments.
        data = struct.pack(">II", 1, 1) + arguments
        buffer = ctypes.create_string_buffer(data)
        parameters = Parameters(ctypes.addressof(buffer), len(data), 4, 0)
        status = dispatch(magic, ctypes.byref(parameters))
        result = struct.unpack(">i", buffer.raw[:4])[0] if status >= 0 else None
        print(f"function=0x{magic:08x} dispatch_status={status} result_status={result}", flush=True)
        return status if status < 0 else result

    if call(0x12DB31C5) < 0:
        raise SystemExit(1)
    try:
        code = ctypes.c_uint32(0xFFFFFFFF)
        # -2 selects production machine provisioning, not a user's DSID.
        status = call(0x385B2CE8, struct.pack(">qQ", -2, ctypes.addressof(code)))
        if status == -45019:
            print("six_digit_generation_supported=false reason=unimplemented-call")
            return 2
        print("six_digit_output_written=" + str(status >= 0 and code.value <= 999999).lower())
        print("A successful call alone would not verify device identity or Apple acceptance.")
        return 0 if status >= 0 and code.value <= 999999 else 1
    finally:
        call(0x08C0FE9E)


if __name__ == "__main__":
    raise SystemExit(main())
