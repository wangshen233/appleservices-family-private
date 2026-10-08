"""Provision an account-isolated diagnostic Mac APS state through the pinned module.

Uses no Apple account credentials, terms, or SMS. The supplied profile must be
an explicitly selected private diagnostic copy. Native messages are silenced;
only status and lengths are printed. Apple writes certificate/token state into
that private profile on a successful APS connection.
"""
import argparse
import base64
import ctypes as C
import json
import msvcrt
import os
from pathlib import Path
import plistlib
import struct
import sys


def main():
    ap = argparse.ArgumentParser(description=__doc__)
    ap.add_argument("--profile", type=Path, required=True)
    ap.add_argument("--library", type=Path, required=True)
    ap.add_argument("--go-state-out", type=Path, help="new private APS state for the Go courier client")
    args = ap.parse_args()
    profile = args.profile.resolve(strict=True)
    if args.go_state_out and args.go_state_out.exists():
        raise SystemExit("Go APS output must be a new private path")
    output = os.fdopen(os.dup(sys.stdout.fileno()), "w", encoding="utf-8", buffering=1)
    null_fd = os.open(os.devnull, os.O_RDWR)
    os.dup2(null_fd, sys.stdout.fileno())
    os.dup2(null_fd, sys.stderr.fileno())
    kernel = C.WinDLL("kernel32")
    kernel.SetStdHandle.argtypes = [C.c_uint32, C.c_void_p]
    kernel.SetStdHandle.restype = C.c_int
    kernel.SetStdHandle(C.c_uint32(-11), msvcrt.get_osfhandle(null_fd))
    kernel.SetStdHandle(C.c_uint32(-12), msvcrt.get_osfhandle(null_fd))
    try:
        hw = plistlib.loads((profile / "hw_info.plist").read_bytes())
        if not hw.get("appleservices_diagnostic", {}).get("synthetic_board_fields"):
            raise RuntimeError("only explicit diagnostic profiles are supported")
        sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "nativeadi"))
        from bridge import Bridge, encode_string
        b = Bridge(str(args.library.resolve(strict=True)))
        result = b.sync(166, encode_string(str(profile)))
        if not result or result[0] != 1:
            raise RuntimeError("bound hardware unavailable")
        hardware = b.handle(result[1:], "SavedHardwareState")
        config = b.handle(b.sync(18, hardware.pack()), "JoinedOSConfig")
        identity = b.handle(b.sync(17, hardware.pack()), "IDSNGMIdentity")
        # Reuse a certificate already issued for this profile across restarts.
        # A token without a keypair is the legacy Windows daemon input: do not
        # import it into a new Mac certificate's connection.
        if hw.get("push", {}).get("keypair"):
            state = b.handle(b.sync(19, hardware.pack()), "APSState")
            state_arg = b"\x01" + state.pack(owned=True)
        else:
            state_arg = b"\x00"
        result = b.call(201, config.pack() + identity.pack() + state_arg + encode_string(str(profile)))
        if len(result) < 13 or result[12] != 0:
            output.write(json.dumps({"stage": "Mac APS setup", "connected": False}) + "\n")
            return 1
        fresh = plistlib.loads((profile / "hw_info.plist").read_bytes())
        push = fresh.get("push", {})
        token = push.get("token") or b""
        # The native serializer omits application diagnostics; retain the marker.
        fresh["appleservices_diagnostic"] = hw["appleservices_diagnostic"]
        (profile / "hw_info.plist").write_bytes(plistlib.dumps(fresh))
        if args.go_state_out:
            from cryptography import x509
            from cryptography.hazmat.primitives import serialization
            keypair = push["keypair"]
            key = serialization.load_der_private_key(keypair["private"], password=None)
            cert = x509.load_der_x509_certificate(keypair["cert"])
            if key.public_key().public_numbers() != cert.public_key().public_numbers():
                raise RuntimeError("Mac push key/certificate mismatch")
            state = {"CertificatePEM": cert.public_bytes(serialization.Encoding.PEM),
                     "PrivateKeyPEM": key.private_bytes(serialization.Encoding.PEM, serialization.PrivateFormat.TraditionalOpenSSL, serialization.NoEncryption()),
                     "Token": token}
            with args.go_state_out.open("x", encoding="utf-8") as f:
                json.dump({k: base64.b64encode(v).decode("ascii") for k, v in state.items()}, f)
        output.write(json.dumps({"stage": "Mac APS setup", "connected": True,
                                 "token_bytes": len(token), "keypair_present": bool(push.get("keypair")),
                                 "token_unchanged_from_input": token == hw.get("push", {}).get("token"),
                                 "go_state_written": bool(args.go_state_out)}) + "\n")
        return 0 if len(token) == 32 and push.get("keypair") else 1
    except Exception as failure:
        output.write(json.dumps({"stage": "Mac APS setup", "failed": type(failure).__name__}) + "\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
