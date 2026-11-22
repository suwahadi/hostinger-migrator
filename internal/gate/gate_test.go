package gate

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"hostinger-migrator/internal/store"
)

func TestGate(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "m.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	st.CreateBatch(ctx, store.Batch{SourceAlias: "a", SourceAccount: "u1", DestAlias: "b", DestAccount: "u2"}, nil)
	bs, _ := st.ListBatches(ctx)
	id := bs[0].ID
	if _, err := For(ctx, st, id, KindCutover, "a.com"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("err = %v", err)
	}
	if err := Request(ctx, st, id, KindCutover, []string{"a.com"}, "cutover"); !errors.Is(err, ErrNotApproved) {
		t.Fatalf("kata salah harus ditolak: %v", err)
	}
	if err := Request(ctx, st, id, KindCutover, nil, "CUTOVER"); err == nil {
		t.Fatal("tanpa website harus ditolak")
	}
	if err := Request(ctx, st, id, KindCutover, []string{"a.com"}, "CUTOVER"); err != nil {
		t.Fatal(err)
	}
	a, err := For(ctx, st, id, KindCutover, "a.com")
	if err != nil || a.Require(KindCutover, "a.com") != nil || a.Require(KindCleanup, "a.com") == nil || a.Require(KindCutover, "b.com") == nil {
		t.Fatalf("approval = %+v %v", a, err)
	}
	var nilA *Approval
	if nilA.Require(KindCutover, "a.com") == nil || Record(ctx, st, nil, "x", "y", "z", "") == nil {
		t.Fatal("approval nil harus ditolak")
	}
	if err := Record(ctx, st, a, "dns.update", "a.com", "OK", "A → 1.2.3.4"); err != nil {
		t.Fatal(err)
	}
	if _, err := For(ctx, st, id, KindCleanup, "a.com"); !errors.Is(err, ErrNotApproved) {
		t.Fatal("jenis lain tidak boleh ikut disetujui")
	}
}
