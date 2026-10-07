"""Generate synthetic Circle SRP/AEAD fixtures independently of the Go code.

Uses the RFC 5054 3072-bit group from the inspected public Rust implementation
and Python's big integers and cryptography AEAD. Never reads account state.
"""
import hashlib
import json
from pathlib import Path
from cryptography.hazmat.primitives.ciphers.aead import AESGCM
from cryptography.hazmat.primitives.kdf.hkdf import HKDF
from cryptography.hazmat.primitives import hashes

def h(*parts):
    return hashlib.sha256(b"".join(parts)).digest()

def integer(n):
    return n.to_bytes(max(1,(n.bit_length()+7)//8),"big")

group=Path(".cache/apple-private-apis/icloud-auth/rustcrypto-srp/src/groups/3072.bin").read_bytes()
N=int.from_bytes(group,"big")
identity,password=b"123456789",b"012345"
salt=bytes(range(16));a=bytes(range(1,33));b=bytes(range(33,65))
A=pow(5,int.from_bytes(a,"big"),N)
x=int.from_bytes(h(salt,h(identity+b":"+password)),"big")
v=pow(5,x,N)
k=int.from_bytes(h(group,5 .to_bytes(384,"big")),"big")
B=(k*v+pow(5,int.from_bytes(b,"big"),N))%N
u=int.from_bytes(h(integer(A),integer(B)),"big")
# Compute independently from the CLIENT side, not the Go server formula.
S=pow((B-k*pow(5,x,N))%N,int.from_bytes(a,"big")+u*x,N)
K=h(integer(S));xor=bytes(a^b for a,b in zip(h(group),h(5 .to_bytes(384,"big"))))
M1=h(xor,h(identity),salt,integer(A),integer(B),K);M2=h(integer(A),M1,K)
key=HKDF(algorithm=hashes.SHA256(),length=16,salt=None,info=b"recv->send").derive(K)
nonce=bytes(range(16,32));plaintext=b"\x0c\x06098765"
encrypted=AESGCM(key).encrypt(nonce,plaintext,None)
fixture={"source":"Python client-side modular arithmetic; cryptography AESGCM/HKDF", "identity":identity.decode(),"password":password.decode()}
fixture.update({name:value.hex() for name,value in {"salt":salt,"b":b,"a_public":integer(A),"b_public":integer(B),"key":K,"m1":M1,"m2":M2,"nonce":nonce,"aead_key":key,"encrypted":encrypted,"plaintext":plaintext}.items()})
target=Path("internal/circle/testdata/reference.json");target.parent.mkdir(parents=True,exist_ok=True);target.write_text(json.dumps(fixture,indent=2)+"\n",encoding="utf-8")
print("Synthetic Circle reference written; no account state used.")
