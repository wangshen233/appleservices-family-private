"""Locate diagnostic string references and enclosing x64 functions offline."""
import argparse
import bisect
import json
import re
from pathlib import Path
import pefile

p = argparse.ArgumentParser()
p.add_argument("binary", type=Path)
p.add_argument("assembly", type=Path)
p.add_argument("--pattern", required=True)
p.add_argument("--function", type=lambda x: int(x, 16))
p.add_argument("--summary", action="store_true")
a = p.parse_args()
pe = pefile.PE(str(a.binary))
base = pe.OPTIONAL_HEADER.ImageBase
functions = sorted((base + x.struct.BeginAddress, base + x.struct.EndAddress) for x in pe.DIRECTORY_ENTRY_EXCEPTION)
starts = [x[0] for x in functions]
imports = {x.address: x.name.decode(errors="replace") for d in pe.DIRECTORY_ENTRY_IMPORT for x in d.imports if x.name}
strings = {}
pattern = re.compile(a.pattern, re.I)
for s in pe.sections:
    if s.Characteristics & 0x20000000:
        continue
    data = s.get_data()
    for regex, enc in [(rb"[\x20-\x7e]{5,}", "ascii"), (rb"(?:[\x20-\x7e]\x00){5,}", "utf-16le")]:
        for m in re.finditer(regex, data):
            value = m.group().decode(enc)
            if len(value) < 400 and pattern.search(value):
                strings[base + s.VirtualAddress + m.start()] = value

def owner(addr):
    i = bisect.bisect_right(starts, addr) - 1
    return functions[i] if i >= 0 and addr < functions[i][1] else None

if a.function is not None:
    selected = owner(a.function)
    if selected:
        a.function = selected[0]

assembly = a.assembly.read_text(encoding="utf-8-sig").splitlines()
# Apple uses initializer functions to create global CFString constants.
# Resolve only short initializer functions whose literal and global write agree.
initializers = {}
for line in assembly:
    m = re.match(r"([0-9a-f]+):", line)
    if not m:
        continue
    addr = int(m[1], 16)
    f = owner(addr)
    if not f or f[1] - f[0] > 96:
        continue
    refs = [int(x,16) for x in re.findall(r"# (0x[0-9a-f]+)", line)]
    for ref in refs:
        if ref in strings and "leaq" in line:
            initializers[f[0]] = strings[ref]
        elif f[0] in initializers and re.search(r"movq\s+%rax,.*\(%rip\)", line):
            strings[ref] = initializers[f[0]]

for line in assembly:
    m = re.match(r"([0-9a-f]+):", line)
    if not m:
        continue
    addr = int(m[1], 16)
    refs = [int(x,16) for x in re.findall(r"# (0x[0-9a-f]+)", line)]
    f = owner(addr)
    if a.function is not None and (not f or f[0] != a.function):
        continue
    for ref in refs:
        if ref in strings:
            print(json.dumps({"string": strings[ref], "reference": hex(addr), "function": hex(f[0]) if f else None}))
    if a.function is not None and f and f[0] == a.function:
        annotation = ", ".join(imports[x] if x in imports else strings[x] for x in refs if x in imports or x in strings)
        if not a.summary or annotation or "callq" in line:
            print(line + (" ; " + annotation if annotation else ""))
