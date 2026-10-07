package gsa

import (
	"bytes"
	"encoding/base64"
	"howett.net/plist"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCircleHeartbeatAuthenticationAndServerAcknowledgement(t *testing.T) {
	calls := 0
	response := map[string]any{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("X-Apple-HB-Token") != base64.StdEncoding.EncodeToString([]byte("account:heartbeat")) {
			t.Error("wrong account authentication")
		}
		if r.Header.Get("X-Apple-I-UrlSwitch-Info") != base64.StdEncoding.EncodeToString([]byte("account:circle")) {
			t.Error("wrong Circle routing")
		}
		body, _ := io.ReadAll(r.Body)
		var packet map[string]any
		if _, err := plist.Unmarshal(body, &packet); err != nil {
			t.Fatal(err)
		}
		request := packet["Request"].(map[string]any)
		if request["atxid"] != "transaction" || request["idmsdata"] != "idms" || request["pakedata"] != "test-DER" || request["ptkn"] != "ABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABABAB" {
			t.Error("wrong transaction binding or token encoding")
		}
		if _, ok := request["ec"]; ok {
			t.Error("unexpected error in successful request")
		}
		body, _ = plist.Marshal(response, plist.XMLFormat)
		w.Write(body)
	}))
	defer server.Close()
	saved := circleServiceURL
	circleServiceURL = server.URL
	defer func() { circleServiceURL = saved }()
	c := NewClient(deviceTestAnisette{})
	payload := map[string]any{"adsid": "account", "t": map[string]any{"com.apple.gs.idms.hb": map[string]any{"token": "heartbeat"}}}
	reply := CircleReply{Transaction: "transaction", IDMSData: "idms", Step: 1, PAKE: "test-DER", PushToken: bytes.Repeat([]byte{0xab}, 32)}
	if err := c.RespondToCircle(map[string]any{}, reply); err == nil || calls != 0 {
		t.Fatal("request sent without authentication")
	}
	if err := c.RespondToCircle(payload, reply); err != nil {
		t.Fatal("valid empty acknowledgement rejected", err)
	}
	response = map[string]any{"ec": int64(-9003)}
	if err := c.RespondToCircle(payload, reply); err == nil {
		t.Fatal("server rejection accepted")
	}
	response = map[string]any{"Response": map[string]any{"Status": map[string]any{"ec": int64(-1)}}}
	if err := c.RespondToCircle(payload, reply); err == nil {
		t.Fatal("nested server rejection accepted")
	}
	reply.Step = 4
	if err := c.RespondToCircle(payload, reply); err == nil || calls != 3 {
		t.Fatal("unsupported response transmitted")
	}
}
