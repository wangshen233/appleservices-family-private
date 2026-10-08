"""Read-only PE inspection of installed AOSKit; never loads or calls the DLL.

Requires pefile and capstone. Addresses are resolved from the inspected file,
not copied from another iCloud version. No account state is read.
"""
import argparse
import bisect
import hashlib
import json
import re
from pathlib import Path

import capstone
import pefile
from capstone.x86 import X86_OP_IMM, X86_OP_MEM, X86_REG_RIP


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("dll", type=Path)
    parser.add_argument("--function", action="append", default=[], help="VA or exact export")
    parser.add_argument("--query", action="append", default=[], help="string or export substring")
    parser.add_argument("--range", action="append", default=[], help="explicit VA start:end, including unwind fragments")
    args = parser.parse_args()
    raw = args.dll.read_bytes()
    pe = pefile.PE(data=raw)
    base = pe.OPTIONAL_HEADER.ImageBase
    exports = {base + s.address: s.name.decode() for s in getattr(pe, "DIRECTORY_ENTRY_EXPORT", type("Empty", (), {"symbols": []})()).symbols if s.name}
    imports = {s.address: s.name.decode(errors="replace") for d in pe.DIRECTORY_ENTRY_IMPORT for s in d.imports if s.name}
    strings = {}
    for section in pe.sections:
        for m in re.finditer(rb"[\x20-\x7e]{4,}\x00", section.get_data()):
            if len(m.group()) < 400:
                strings[base + section.VirtualAddress + m.start()] = m.group()[:-1].decode()
        for m in re.finditer(rb"(?:[\x20-\x7e]\x00){4,}", section.get_data()):
            if len(m.group()) < 800:
                strings[base + section.VirtualAddress + m.start()] = m.group().decode("utf-16le")
    funcs = sorted((base + f.struct.BeginAddress, base + f.struct.EndAddress) for f in pe.DIRECTORY_ENTRY_EXCEPTION)
    # Leaf exports can omit unwind entries. Decode them only up to their
    # terminating return/tail jump, rather than attributing the next function.
    for va in exports:
        if not any(start <= va < end for start, end in funcs):
            if any(s.Characteristics & 0x20000000 and base + s.VirtualAddress <= va < base + s.VirtualAddress + s.Misc_VirtualSize for s in pe.sections):
                probe = capstone.Cs(capstone.CS_ARCH_X86, capstone.CS_MODE_64)
                for ins in probe.disasm(pe.get_data(va - base, 128), va):
                    if ins.mnemonic in ("ret", "jmp"):
                        funcs.append((va, ins.address + ins.size))
                        break
    funcs.sort()
    starts = [f[0] for f in funcs]

    def owner(va):
        i = bisect.bisect_right(starts, va) - 1
        return funcs[i] if i >= 0 and va < funcs[i][1] else None

    md = capstone.Cs(capstone.CS_ARCH_X86, capstone.CS_MODE_64)
    md.detail = True
    instructions = []
    references = {}
    by_function = {}
    for start, end in funcs:
        part = []
        for ins in md.disasm(pe.get_data(start - base, end - start), start):
            refs = []
            for op in ins.operands:
                if op.type == X86_OP_MEM and op.mem.base == X86_REG_RIP:
                    refs.append(ins.address + ins.size + op.mem.disp)
                elif op.type == X86_OP_IMM and ins.mnemonic in ("call", "jmp"):
                    refs.append(op.imm)
            instructions.append((ins.address, ins.mnemonic, ins.op_str, refs))
            part.append(instructions[-1])
            for ref in set(refs):
                references.setdefault(ref, []).append(ins.address)
        by_function[start] = part

    # Resolve exported CFString globals via small initializer functions.
    # This maps a stored CFString pointer to its source literal without executing it.
    constants = {}
    for start, end in funcs:
        if end - start > 80:
            continue
        part = by_function[start]
        literals = [strings[r] for _, mn, _, refs in part if mn == "lea" for r in refs if r in strings]
        stores = [r for _, mn, op, refs in part if mn == "mov" and op.startswith("qword ptr [rip") for r in refs if r in exports]
        if len(literals) == 1 and len(stores) == 1:
            constants[stores[0]] = literals[0]

    def label(va):
        if va in constants:
            return exports.get(va, hex(va)) + "=" + repr(constants[va])
        return imports.get(va) or exports.get(va) or (repr(strings[va]) if va in strings else hex(va))

    report = {"file": str(args.dll), "sha256": hashlib.sha256(raw).hexdigest(), "image_base": hex(base), "function_scope": "PE unwind entries; a function may span multiple entries", "queries": [], "functions": []}
    for query in args.query:
        targets = {va for source in (strings, exports, imports, constants) for va, s in source.items() if query.lower() in s.lower()}
        for va in sorted(targets):
            report["queries"].append({"target": hex(va), "label": label(va), "references": [{"instruction": hex(r), "function": hex(owner(r)[0]) if owner(r) else None} for r in references.get(va, [])]})
    for requested in args.function:
        va = int(requested, 16) if requested.startswith("0x") else next((v for v, n in exports.items() if n == requested), None)
        bounds = owner(va) if va is not None else None
        if not bounds:
            raise SystemExit("Function not found: " + requested)
        start, end = bounds
        report["functions"].append({"start": hex(start), "end": hex(end), "lines": [f"{hex(addr)}: {mn} {op}" + (" ; " + " | ".join(label(r) for r in refs) if refs else "") for addr, mn, op, refs in instructions if start <= addr < end]})
    for span in args.range:
        start, end = (int(v, 16) for v in span.split(":"))
        if not base <= start < end <= base + pe.OPTIONAL_HEADER.SizeOfImage:
            raise SystemExit("Range outside image")
        lines = []
        for ins in md.disasm(pe.get_data(start - base, end - start), start):
            refs = [ins.address + ins.size + op.mem.disp for op in ins.operands if op.type == X86_OP_MEM and op.mem.base == X86_REG_RIP]
            lines.append(f"{hex(ins.address)}: {ins.mnemonic} {ins.op_str}" + (" ; " + " | ".join(label(r) for r in refs) if refs else ""))
        report["functions"].append({"start": hex(start), "end": hex(end), "explicit_range": True, "lines": lines})
    print(json.dumps(report, ensure_ascii=False, indent=2))


if __name__ == "__main__":
    main()
