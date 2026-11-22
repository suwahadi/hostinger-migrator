package cutover

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
)

var rollbackable = map[string]bool{
	pipeline.StateCutoverRunning: true, pipeline.StateCutoverDone: true, pipeline.StateSSLActive: true, pipeline.StateSSLPending: true,
	pipeline.StateEmailRunning: true, pipeline.StateEmailDone: true, pipeline.StateNoEmail: true, pipeline.StateCleanupReady: true,
}

type RollbackResult struct {
	DNSRestored   []string
	ParkedRemoved bool
	Manual        []string
}

func Rollback(ctx context.Context, r *pipeline.Runner, it *store.PipelineItem) (*RollbackResult, error) {
	if !rollbackable[it.State] {
		return nil, fmt.Errorf("rollback hanya untuk website yang sudah memulai cutover dan belum cleanup (status sekarang %s)", it.State)
	}
	if it.EmailState != "" && it.EmailState != "BELUM" {
		return nil, errors.New("email domain ini sudah mulai dipindah; rollback otomatis tidak aman, lihat panduan manual di README")
	}
	b, _, err := r.Store.GetBatch(ctx, it.BatchID)
	if err != nil {
		return nil, err
	}
	s := &Stage{Runner: r}
	var cp checkpoint
	steps, _ := r.Store.ListSteps(ctx, it.ID)
	for _, st := range steps {
		if st.Name == "cutover" && st.Checkpoint != "" {
			json.Unmarshal([]byte(st.Checkpoint), &cp)
		}
	}
	res := &RollbackResult{}
	if len(cp.DNSChanged) > 0 && cp.DNSChanged[0] != "manual" {
		api, err := r.Client(b.SourceAlias)
		if err != nil {
			return nil, err
		}
		restored, err := s.restoreDNS(ctx, api, it.SourceDomain, cp.SnapshotID)
		if err != nil {
			return nil, err
		}
		res.DNSRestored = restored
	} else if len(cp.DNSChanged) > 0 {
		res.Manual = append(res.Manual, "Kembalikan record A/AAAA/www di penyedia DNS eksternal ke nilai sebelum cutover (lihat laporan DNS sebelum/sesudah).")
	}
	if cp.Parked && it.DummyDomain != "" {
		api, err := r.Client(b.DestAlias)
		if err != nil {
			return nil, err
		}
		_, err = api.Do(ctx, hostinger.Request{Endpoint: "hosting.parked_domains.delete", PathParams: map[string]string{"username": b.DestAccount, "domain": it.DummyDomain, "parkedDomain": it.SourceDomain}}, nil)
		if err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			res.Manual = append(res.Manual, fmt.Sprintf("Hapus parked domain %s dari website %s di akun %s (API gagal: %v).", it.SourceDomain, it.DummyDomain, b.DestAccount, err))
		} else {
			res.ParkedRemoved = true
		}
	}
	if it.SourceTempDomain != "" {
		res.Manual = append(res.Manual, fmt.Sprintf("hPanel akun %s → Websites → %s → ganti domain kembali ke %s.", b.SourceAccount, it.SourceTempDomain, it.SourceDomain))
	}
	if err := r.Store.RevokeApproval(ctx, it.BatchID, "CUTOVER", it.SourceDomain); err != nil {
		return nil, err
	}
	r.Store.DeleteManualTasks(ctx, it.ID, "MANUAL_SOURCE_RENAME", "MANUAL_DEST_DOMAIN", "MANUAL_DNS")
	r.Store.DeleteStep(ctx, it.ID, "cutover")
	_, err = r.Store.UpdateItem(ctx, it.ID, "operator", "cutover.rollback", func(x *store.PipelineItem) error {
		x.State = pipeline.StateVerified
		x.SideState = ""
		x.ManualCode = ""
		x.LastError = "di-rollback; jalankan 'migrator cutover' lagi bila siap"
		x.SourceTempDomain = ""
		return nil
	})
	return res, err
}

func (s *Stage) restoreDNS(ctx context.Context, api *hostinger.Client, domain string, snapshotID int64) ([]string, error) {
	snaps, err := s.Runner.Store.ListDNSSnapshots(ctx, domain)
	if err != nil {
		return nil, err
	}
	var snap *store.DNSSnapshot
	for i := range snaps {
		if snaps[i].ID == snapshotID {
			snap = &snaps[i]
		}
	}
	if snap == nil {
		return nil, fmt.Errorf("snapshot DNS lokal %d untuk %s tidak ditemukan", snapshotID, domain)
	}
	var original []hostinger.DNSRecord
	if err := json.Unmarshal([]byte(snap.RecordsJSON), &original); err != nil {
		return nil, fmt.Errorf("snapshot DNS rusak: %w", err)
	}
	current, err := api.GetZone(ctx, domain)
	if err != nil {
		return nil, err
	}
	managed := func(name, typ string) bool {
		return (name == "@" || name == "www") && (typ == "A" || typ == "AAAA" || typ == "CNAME")
	}
	type key struct{ name, typ string }
	orig := map[key]hostinger.DNSRecord{}
	for _, rr := range original {
		if managed(rr.Name, rr.Type) {
			orig[key{rr.Name, rr.Type}] = rr
		}
	}
	var deletes []map[string]string
	var restored []string
	for _, rr := range current {
		if managed(rr.Name, rr.Type) {
			if _, ok := orig[key{rr.Name, rr.Type}]; !ok {
				deletes = append(deletes, map[string]string{"name": rr.Name, "type": rr.Type})
				restored = append(restored, "hapus "+rr.Type+" "+rr.Name)
			}
		}
	}
	if len(deletes) > 0 {
		if _, err := api.Do(ctx, hostinger.Request{Endpoint: "dns.zones.delete_records", PathParams: map[string]string{"domain": domain}, Body: map[string]any{"filters": deletes}}, nil); err != nil {
			return nil, err
		}
	}
	var zone []map[string]any
	for k, rr := range orig {
		var recs []map[string]any
		var vals []string
		for _, r := range rr.Records {
			recs = append(recs, map[string]any{"content": r.Content})
			vals = append(vals, r.Content)
		}
		zone = append(zone, map[string]any{"name": rr.Name, "type": rr.Type, "ttl": rr.TTL, "records": recs})
		restored = append(restored, k.typ+" "+k.name+" → "+strings.Join(vals, ", "))
	}
	if len(zone) > 0 {
		if _, err := api.Do(ctx, hostinger.Request{Endpoint: "dns.zones.update", PathParams: map[string]string{"domain": domain}, Body: map[string]any{"overwrite": true, "zone": zone}}, nil); err != nil {
			return nil, err
		}
	}
	s.Runner.Store.AppendAudit(ctx, nil, "operator", "dns.rollback", domain, "OK", strings.Join(restored, "; "))
	return restored, nil
}
