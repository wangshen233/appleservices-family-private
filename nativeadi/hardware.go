package nativeadi

import "crypto/sha256"

// SyntheticMACAddress derives a stable, locally administered unicast address
// for a diagnostic device. It is not an address exported from real Mac hardware.
func SyntheticMACAddress(identifier []byte) []byte {
	input := append([]byte("appleservices-mac-network-v1\x00"), identifier...)
	sum := sha256.Sum256(input)
	address := append([]byte(nil), sum[:6]...)
	address[0] = (address[0] | 2) & 0xfe
	return address
}
