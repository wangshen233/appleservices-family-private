package main

import (
	"crypto/sha256"
	"fmt"
	"github.com/Laky-64/appleservices"
	"github.com/Laky-64/appleservices/internal/control"
)

func requestID(transaction string) string {
	sum := sha256.Sum256([]byte(transaction))
	return fmt.Sprintf("%x", sum[:16])
}
func panelDevices(devices []appleservices.AccountDevice, name string) []control.Device {
	result := make([]control.Device, 0, len(devices))
	for _, device := range devices {
		result = append(result, control.Device{Name: device.Name, Model: device.ModelName, Current: device.CurrentDevice, Installation: device.Name == name, Trusted: device.IsTrustedDevice})
	}
	return result
}
