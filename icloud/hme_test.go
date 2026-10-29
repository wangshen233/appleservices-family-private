package icloud

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
)

func TestHideMyEmailLifecycleBindsCurrentAlias(t *testing.T) {
	var mu sync.Mutex
	active := true
	paths := []string{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("dsid") != "123" || r.URL.Query().Get("clientBuildNumber") != hmeBuildNumber {
			http.Error(w, `{"success":false}`, http.StatusBadRequest)
			return
		}
		mu.Lock()
		paths = append(paths, r.URL.Path)
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v2/hme/list":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"hmeEmails": []map[string]any{{"hme": "alias@icloud.com", "label": "shop", "note": "test", "isActive": active, "anonymousId": "private-id"}}}})
		case "/v1/hme/generate":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{"hme": "candidate@icloud.com"}})
		case "/v1/hme/reserve", "/v1/hme/updateMetaData", "/v1/hme/reactivate", "/v1/hme/delete":
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		case "/v1/hme/deactivate":
			active = false
			_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": map[string]any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	jar, _ := cookiejar.New(nil)
	client := &HideMyEmailClient{
		appleID:     "member@example.test",
		sessionPath: filepath.Join(t.TempDir(), "hme-session.json"),
		http:        server.Client(),
		jar:         jar,
		session:     hmeSession{AppleID: "member@example.test", DSID: "123", ClientID: "client", ServiceURL: server.URL},
	}
	client.http.Jar = jar
	ctx := context.Background()
	info, err := client.List(ctx)
	if err != nil || info.ActiveCount != 1 || len(info.Aliases) != 1 || info.Aliases[0].anonymousID != "private-id" {
		t.Fatalf("unexpected list info=%#v err=%v", info, err)
	}
	candidate, err := client.Generate(ctx, "zh-cn")
	if err != nil || candidate != "candidate@icloud.com" {
		t.Fatalf("unexpected candidate=%q err=%v", candidate, err)
	}
	if err := client.Reserve(ctx, candidate, "label", "note"); err != nil {
		t.Fatal(err)
	}
	if err := client.Update(ctx, "alias@icloud.com", "updated", "updated note"); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, "alias@icloud.com"); err == nil {
		t.Fatal("active alias was deleted")
	}
	if err := client.SetActive(ctx, "alias@icloud.com", false); err != nil {
		t.Fatal(err)
	}
	if err := client.Delete(ctx, "alias@icloud.com"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(paths) < 8 || paths[len(paths)-1] != "/v1/hme/delete" {
		t.Fatalf("unexpected request paths %v", paths)
	}
}

func TestHideMyEmailSafeAliasOmitsPrivateIdentifier(t *testing.T) {
	data, err := json.Marshal(HideMyEmailAlias{Email: "alias@icloud.com", anonymousID: "private-id"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"email":"alias@icloud.com","active":false}` {
		t.Fatalf("private identifier leaked in %s", data)
	}
}
