package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"hostinger-migrator/internal/logx"
)

func TestReportJSONValid(t *testing.T) {
	dir := t.TempDir()
	rep := &Report{SSHKey: []SSHRow{{Akun: "u1", Alias: "sumber", Status: "SSH_READY", Login: "key"}}}
	if _, err := (&Builder{Redactor: logx.NewRedactor()}).Write(rep, dir, map[string]bool{"json": true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "laporan.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(data) {
		t.Fatalf("laporan.json tidak valid:\n%s", data)
	}
}
