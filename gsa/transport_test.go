package gsa

import (
	"net/http"
	"testing"
)

func TestAppleTransportPreservesProxyAndCertificateVerification(t *testing.T) {
	transport := AppleRootTransport().(*http.Transport)
	if transport.Proxy == nil {
		t.Fatal("Apple requests bypass configured environment proxy")
	}
	if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil || transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("Apple certificate verification is not enabled")
	}
}
