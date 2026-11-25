package inventory

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/store"
)

func TestWriteJSONStaysValid(t *testing.T) {
	r := logx.NewRedactor()
	r.Add("20251125")
	r.Add("rahasiaDB123")
	s := &Scanner{DataDir: t.TempDir(), Redactor: r}
	p, err := s.writeJSON(&store.InventorySnapshot{
		Alias: "x", AccountID: "u1",
		Websites: []store.Website{{Domain: "a.com", SizeBytes: 9920251125}},
		Cron:     []store.CronRow{{Schedule: "* * * * *", Command: "mysqldump -prahasiaDB123 db"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	s2 := string(data)
	if !json.Valid(data) || !strings.Contains(s2, "9920251125") || strings.Contains(s2, "rahasiaDB123") || !strings.Contains(s2, logx.Mask) {
		t.Fatalf("JSON tidak valid atau rahasia tidak tersaring:\n%s", s2)
	}
}
