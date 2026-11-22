package gate

import (
	"context"
	"errors"
	"fmt"

	"hostinger-migrator/internal/store"
)

const (
	KindCutover     = "CUTOVER"
	KindDeleteEmail = "HAPUS_EMAIL"
	KindCleanup     = "CLEANUP"
)

var Words = map[string]string{
	KindCutover:     "CUTOVER",
	KindDeleteEmail: "HAPUS EMAIL",
	KindCleanup:     "HAPUS",
}

var ErrNotApproved = errors.New("operasi destruktif belum disetujui operator")

type Approval struct {
	batchID string
	kind    string
	domain  string
}

func (a *Approval) BatchID() string { return a.batchID }
func (a *Approval) Kind() string    { return a.kind }
func (a *Approval) Domain() string  { return a.domain }

func Request(ctx context.Context, st *store.Store, batchID, kind string, domains []string, typed string) error {
	word, ok := Words[kind]
	if !ok {
		return fmt.Errorf("jenis persetujuan %q tidak dikenal", kind)
	}
	if typed != word {
		return fmt.Errorf("%w: konfirmasi %q tidak sesuai", ErrNotApproved, word)
	}
	if len(domains) == 0 {
		return errors.New("tidak ada website untuk disetujui")
	}
	return st.SaveApproval(ctx, batchID, kind, domains)
}

func For(ctx context.Context, st *store.Store, batchID, kind, domain string) (*Approval, error) {
	ok, err := st.IsApproved(ctx, batchID, kind, domain)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s untuk %s di batch %s", ErrNotApproved, kind, domain, batchID)
	}
	return &Approval{batchID: batchID, kind: kind, domain: domain}, nil
}

func (a *Approval) Require(kind, domain string) error {
	if a == nil || a.kind != kind || a.domain != domain {
		return fmt.Errorf("%w: %s untuk %s", ErrNotApproved, kind, domain)
	}
	return nil
}

func Record(ctx context.Context, st *store.Store, a *Approval, action, target, result, detail string) error {
	if a == nil {
		return ErrNotApproved
	}
	return st.AppendAudit(ctx, nil, "worker", action, target, result, "persetujuan "+a.kind+" batch "+a.batchID+": "+detail)
}
