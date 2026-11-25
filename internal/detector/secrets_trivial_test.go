package detector

import (
	"slices"
	"testing"
)

func TestExtractSecretsSkipsTrivialValues(t *testing.T) {
	got := ExtractSecrets("MAIL_PASSWORD=null\nPUSHER_APP_KEY=${PUSHER_KEY}\nAPI_TOKEN=false\nDB_PASSWORD=${Xy!9pass\nSTRIPE_SECRET=sk_live_abc123\n")
	for _, want := range []string{"${Xy!9pass", "sk_live_abc123"} {
		if !slices.Contains(got, want) {
			t.Errorf("rahasia %q harus terdaftar: %v", want, got)
		}
	}
	for _, gone := range []string{"null", "false", "${PUSHER_KEY}"} {
		if slices.Contains(got, gone) {
			t.Errorf("nilai sepele %q tidak boleh terdaftar: %v", gone, got)
		}
	}
}
