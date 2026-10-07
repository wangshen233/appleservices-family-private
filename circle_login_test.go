package appleservices

import "testing"

func TestNumericIdentityAcceptsSPDSpellingsWithoutCoercion(t *testing.T) {
	for _, test := range []struct {
		payload map[string]any
		want    string
	}{
		{map[string]any{"DsPrsId": uint64(123)}, "123"},
		{map[string]any{"dsPrsId": "456"}, "456"},
		{map[string]any{"dsid": int64(789)}, "789"},
		{map[string]any{"prsId": "001"}, ""},
		{map[string]any{"DsPrsId": "account@example.com"}, ""},
	} {
		if got := numericIdentity(test.payload); got != test.want {
			t.Fatalf("identity=%q want %q", got, test.want)
		}
	}
}
