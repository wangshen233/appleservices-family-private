// Package circle implements the IDMS Circle responder for an approved login.
// It exchanges a fresh ADI verification code; it does not possess or issue
// Octagon trust vouchers or account encryption keys.
package circle

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"math/big"
)

const group3072 = "FFFFFFFFFFFFFFFFC90FDAA22168C234C4C6628B80DC1CD129024E088A67CC74020BBEA63B139B22514A08798E3404DDEF9519B3CD3A431B302B0A6DF25F14374FE1356D6D51C245E485B576625E7EC6F44C42E9A637ED6B0BFF5CB6F406B7EDEE386BFB5A899FA5AE9F24117C4B1FE649286651ECE45B3DC2007CB8A163BF0598DA48361C55D39A69163FA8FD24CF5F83655D23DCA3AD961C62F356208552BB9ED529077096966D670C354E4ABC9804F1746C08CA18217C32905E462E36CE3BE39E772C180E86039B2783A2EC07A28FB5C55DF06F4C52C9DE2BCBF6955817183995497CEA956AE515D2261898FA051015728E5A8AAAC42DAD33170D04507A33A85521ABDF1CBA64ECFB850458DBEF0A8AEA71575D060C7DB3970F85A6E1E4C7ABF5AE8CDB0933D71E8C94E04A25619DCEE3D2261AD2EE6BF12FFA06D98A0864D87602733EC86A64521F2B18177B200CBBE117577A615D6C770988C0BAD946E208E24FA074E5AB3143DB5BFCE0FD108E4B82D120A93AD2CAFFFFFFFFFFFFFFFF"

type srpServer struct{ salt, public, key, m1, m2 []byte }

func hash(parts ...[]byte) []byte {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
	}
	return h.Sum(nil)
}

func newSRP(username, password string, publicA []byte) (*srpServer, error) {
	salt, b := make([]byte, 16), make([]byte, 32)
	if _, err := rand.Read(salt); err != nil {
		return nil, err
	}
	if _, err := rand.Read(b); err != nil {
		return nil, err
	}
	return deriveSRP(username, password, publicA, salt, b)
}

func deriveSRP(username, password string, publicA, salt, secretB []byte) (*srpServer, error) {
	n, _ := new(big.Int).SetString(group3072, 16)
	g := big.NewInt(5)
	a := new(big.Int).SetBytes(publicA)
	if len(publicA) > 384 || a.Sign() == 0 || a.Cmp(n) >= 0 {
		return nil, errors.New("circle: invalid SRP public value")
	}
	b := new(big.Int).SetBytes(secretB)
	if b.Sign() == 0 {
		return nil, errors.New("circle: invalid SRP private value")
	}
	paddedG := make([]byte, 384)
	paddedG[383] = 5
	k := new(big.Int).SetBytes(hash(n.Bytes(), paddedG))
	x := new(big.Int).SetBytes(hash(salt, hash([]byte(username+":"+password))))
	v := new(big.Int).Exp(g, x, n)
	B := new(big.Int).Mod(new(big.Int).Add(new(big.Int).Mul(k, v), new(big.Int).Exp(g, b, n)), n)
	// The Circle implementation hashes the integer encodings without padding.
	u := new(big.Int).SetBytes(hash(a.Bytes(), B.Bytes()))
	if u.Sign() == 0 {
		return nil, errors.New("circle: invalid SRP scrambling parameter")
	}
	premaster := new(big.Int).Exp(new(big.Int).Mod(new(big.Int).Mul(a, new(big.Int).Exp(v, u, n)), n), b, n)
	key := hash(premaster.Bytes())
	xor := hash(n.Bytes())
	hg := hash(paddedG)
	for i := range xor {
		xor[i] ^= hg[i]
	}
	m1 := hash(xor, hash([]byte(username)), salt, a.Bytes(), B.Bytes(), key)
	m2 := hash(a.Bytes(), m1, key)
	return &srpServer{salt: append([]byte(nil), salt...), public: B.Bytes(), key: key, m1: m1, m2: m2}, nil
}

func (s *srpServer) verify(proof []byte) bool { return hmac.Equal(proof, s.m1) }
