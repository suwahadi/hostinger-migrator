package pipeline

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/store"
)

func TestBackupManualUnknownSource(t *testing.T) {
	ic := &ItemContext{
		Item:  &store.PipelineItem{SourceDomain: "a.com"},
		Batch: &store.Batch{SourceAccount: "u874"},
		Databases: []store.DatabaseRow{
			{Name: "u874_ok", Source: "public_html/wp-config.php"},
			{Name: "u874_x", Source: "api"},
		},
	}
	err := backupManual(fmt.Errorf("%w: x", backup.ErrUnknownDBSource), ic)
	var m *ManualError
	if !errors.As(err, &m) || m.Code != "MANUAL_DB_SOURCE" {
		t.Fatalf("harus tindakan manual MANUAL_DB_SOURCE: %v", err)
	}
	for _, want := range []string{"u874_x", "a.com", "migrator inventory --akun u874 --refresh", "migrator confirm MANUAL_DB_SOURCE a.com"} {
		if !strings.Contains(m.Instructions, want) {
			t.Errorf("instruksi tidak memuat %q:\n%s", want, m.Instructions)
		}
	}
	if strings.Contains(m.Instructions, "u874_ok") {
		t.Errorf("instruksi tidak boleh menyebut database yang sumbernya diketahui:\n%s", m.Instructions)
	}
	other := errors.New("lain")
	if got := backupManual(other, ic); got != other {
		t.Fatalf("error lain harus dikembalikan apa adanya: %v", got)
	}
}
