# Open-source implementation map

## NAC emulation

- [OpenAbsinthe in mosessupposeshis/imessage](https://github.com/mosessupposeshis/imessage/tree/master/rustpush/open-absinthe) emulates Apple’s x86_64 `IMDAppleServices` with Unicorn and hooks IOKit/CoreFoundation. It needs an explicit hardware profile and does not prove Apple acceptance by itself.
- [JJTech0130/nacserver](https://github.com/JJTech0130/nacserver) publishes the reference `IMDAppleServices` binary and the original Python hook approach. Pin and verify the published hash before executing a binary.
- [JJTech0130/pypush](https://github.com/JJTech0130/pypush/tree/sms-registration/emulated) contains a Python NAC flow with the three calls `NACInit`, `NACKeyEstablishment`, and `NACSign`.

## Windows native ADI

- [Dadoum/apple-crates](https://github.com/Dadoum/apple-crates) documents the Windows CoreADI dispatcher and the provisioning/OTP message ABI. Its Windows support can generate machine anisette, but it is not a complete iCloud MME activation client.
- [rileytestut/AltServer-Windows](https://github.com/rileytestut/AltServer-Windows) is useful for Windows AOSKit/anisette loading patterns. It should not be treated as evidence of a working iCloud delegate exchange.

## Hardware generation

- [corpnewt/GenSMBIOS](https://github.com/corpnewt/GenSMBIOS) and [acidanthera/macserial](https://github.com/acidanthera/OpenCorePkg/tree/master/Utilities/macserial) generate SMBIOS-style values. They do not create an Apple-trusted hardware identity and must not be silently substituted for user-owned hardware data.

## Request protocol

- [OpenBubbles/rustpush auth.rs](https://github.com/OpenBubbles/rustpush/blob/master/src/auth.rs) is a useful reference for the v2 plist shape and `X-Mme-Nas-Qualify` handling. Verify the current source before relying on a field or endpoint.
