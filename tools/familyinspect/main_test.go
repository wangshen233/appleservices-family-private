package main

import (
	"net/url"
	"reflect"
	"testing"
)

func TestSafeFamilyUIFormsOmitsValuesAndResolvesAppleAction(t *testing.T) {
	origin, _ := url.Parse("https://setup.icloud.com/setup/mac/family/setupFamilyUI")
	forms := safeFamilyUIForms(origin, []byte(`<html><form method="post" action="/setup/familykit/macos/addFamilyMember"><input type="hidden" name="familyId" value="private"><input name="email" value="private@example.test"><button type="submit" name="continue" value="private">Continue</button></form></html>`))
	if len(forms) != 1 || forms[0].Method != "POST" || forms[0].Host != "setup.icloud.com" || forms[0].Path != "/setup/familykit/macos/addFamilyMember" {
		t.Fatalf("unexpected safe form summary: %#v", forms)
	}
	want := []string{"hidden:familyId", "input:email", "submit:continue"}
	if !reflect.DeepEqual(forms[0].Fields, want) {
		t.Fatalf("values leaked or fields changed: %#v", forms[0].Fields)
	}
}

func TestSafeFamilyUIScriptsAndIdentifiers(t *testing.T) {
	origin, _ := url.Parse("https://setup.icloud.com/setup/mac/family/setupFamilyUI")
	scripts := safeFamilyUIScripts(origin, []byte(`<base href="/resource/current/family/"><script src="javascript-packed.js"></script><script src="https://evil.example/family.js"></script>`))
	if len(scripts) != 1 || scripts[0].Hostname() != "setup.icloud.com" || scripts[0].EscapedPath() != "/resource/current/family/javascript-packed.js" {
		t.Fatalf("unexpected safe scripts: %#v", scripts)
	}
	want := []string{"addFamilyMember", "finishSettingUpFamily"}
	if got := safeFamilyActionIdentifiers([]byte(`function addFamilyMember(secret){return finishSettingUpFamily(secret)}`)); !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected safe identifiers: %#v", got)
	}
}

func TestSortedMapKeys(t *testing.T) {
	if got, want := sortedMapKeys(map[string]any{"z": 1, "a": 2}), []string{"a", "z"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("unexpected sorted keys: %#v", got)
	}
}

func TestNumericIDValueAcceptsAppleIntegerShapes(t *testing.T) {
	for _, value := range []any{"123", int(123), int64(123), uint64(123), float64(123)} {
		if got := numericIDValue(value); got != "123" {
			t.Fatalf("unexpected numeric ID %q for %T", got, value)
		}
	}
	for _, value := range []any{"", "12x", 0, -1, 1.5} {
		if got := numericIDValue(value); got != "" {
			t.Fatalf("invalid ID accepted: %q for %T", got, value)
		}
	}
}

func TestFamilyResponseErrorRecognizesHTTP200ErrorPayload(t *testing.T) {
	for _, payload := range []map[string]any{
		{"invalid-invite-code-title": "Invalid"},
		{"status-message": "invalid invitation"},
		{"success": false},
	} {
		if !familyResponseError(payload) {
			t.Fatalf("Apple error payload was accepted: %#v", payload)
		}
	}
	if familyResponseError(map[string]any{"success": true, "status-message": "success"}) {
		t.Fatal("success payload was rejected")
	}
}

func TestFindFamilyBoolMatchesOnlyRedactedSharingFlags(t *testing.T) {
	member := map[string]any{
		"member-apple-id":                "private@example.test",
		"shareMyPurchasesEnabledDefault": false,
		"nested": map[string]any{
			"location-sharing-enabled": true,
		},
	}
	purchase, purchaseKnown := findFamilyBool(member, func(key string) bool {
		return key == "sharemypurchasesenableddefault"
	})
	location, locationKnown := findFamilyBool(member, func(key string) bool {
		return key == "locationsharingenabled"
	})
	if !purchaseKnown || purchase || !locationKnown || !location {
		t.Fatalf("unexpected sharing flags purchase=%t/%t location=%t/%t", purchase, purchaseKnown, location, locationKnown)
	}
}

func TestFindFamilyIntegerAcceptsNestedStorageBytes(t *testing.T) {
	value := map[string]any{"storage": map[string]any{"storageInBytes": float64(53687091200)}}
	if got := findFamilyInteger(value, "storageInBytes"); got != 53687091200 {
		t.Fatalf("unexpected storage bytes %d", got)
	}
}
