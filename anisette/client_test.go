package anisette

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestGetHeadersPreservesServiceIdentity(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v3/client_info":
			fmt.Fprint(w, `{"client_info":"test-client","user_agent":"test-agent"}`)
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
	if h["User-Agent"] != "test-agent" || h["X-Mme-Client-Info"] != "test-client" || h["X-Mme-Device-Id"] == "" {
		t.Fatal("lost device identity headers")
	}
}
