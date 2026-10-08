package main

import (
	"fmt"
	"github.com/Laky-64/appleservices/internal/delegateprobe"
	"os"
)

func main() {
	if err := delegateprobe.Run("iosbuddy"); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
