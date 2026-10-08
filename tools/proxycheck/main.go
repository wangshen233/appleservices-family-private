// Command proxycheck reports whether the SOCKS5 setting routes traffic to a
// different egress address. Apple throttles the verification-code page by egress
// address, so knowing which address a request leaves from is the difference
// between a working retry and another rejected one.
package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	"golang.org/x/net/proxy"
)

func main() {
	raw := strings.TrimSpace(os.Getenv("APPLESERVICES_SOCKS_PROXY"))
	fmt.Printf("APPLESERVICES_SOCKS_PROXY = %q\n", raw)

	client := &http.Client{Timeout: 25 * time.Second}
	if raw != "" {
		dialer, err := proxy.SOCKS5("tcp", raw, nil, proxy.Direct)
		if err != nil {
			fmt.Println("socks dialer:", err)
			os.Exit(1)
		}
		client.Transport = &http.Transport{
			DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
				if cd, ok := dialer.(interface {
					DialContext(context.Context, string, string) (net.Conn, error)
				}); ok {
					return cd.DialContext(ctx, network, addr)
				}
				return dialer.Dial(network, addr)
			},
		}
	}
	resp, err := client.Get("https://api.ipify.org")
	if err != nil {
		fmt.Println("egress check:", err)
		os.Exit(1)
	}
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
	resp.Body.Close()
	fmt.Printf("egress address: %s\n", strings.TrimSpace(string(body)))
}
