package anisette

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetHeadersPreservesServiceIdentity(t *testing.T) {
	legacy := "<MacBookPro13,2> <macOS;13.1;22C65> <com.apple.AuthKit/1 (com.apple.dt.Xcode/3594.4.19)>"
	expected := "<MacBookPro13,2> <macOS;13.1;22C65> <com.apple.AuthKit/1 (com.apple.akd/1.0)>"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/client_info":
			fmt.Fprintf(w, `{"client_info":%q,"user_agent":"test-agent"}`, legacy)
		case "/v3/get_headers":
			fmt.Fprint(w, `{"result":"Headers","X-Apple-I-MD":"test-md","X-Apple-I-MD-M":"test-mdm"}`)
		default:
			t.Errorf("unexpected request path: %s", r.URL.Path)
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	c, err := NewClient(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	h, err := c.GetHeaders(NewState())
	if err != nil {
		t.Fatal(err)
	}
	if h["User-Agent"] != "test-agent" || h["X-Mme-Client-Info"] != expected || h["X-Mme-Device-Id"] == "" {
		t.Fatal("lost device identity headers")
	}
	if c.albertHeaders(NewState())["X-Mme-Client-Info"] != expected {
		t.Fatal("provisioning still sends the retired client token")
	}
}
