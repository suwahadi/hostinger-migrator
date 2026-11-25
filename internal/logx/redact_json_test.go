package logx

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestRedactorJSON(t *testing.T) {
	r := NewRedactor()
	r.Add("rahasiaDB123")
	out, err := r.JSON([]byte(`{"token":"sumber","n":9920251125,"c":"x rahasiaDB123"}`))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !json.Valid(out) || !strings.Contains(s, `"token": "sumber"`) || !strings.Contains(s, "9920251125") || strings.Contains(s, "rahasiaDB123") {
		t.Fatalf("hasil salah:\n%s", s)
	}
}
