package cleanup

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
)

type Options struct {
	RequireSSL   bool
	IncludeDummy bool
	Resolve      func(ctx context.Context, host string) ([]string, error)
}

type Check struct {
	Item     *store.PipelineItem
	Eligible bool
	Reasons  []string
}

type Stage struct {
	Runner *pipeline.Runner
	Opts   Options
	Sleep  func(context.Context, time.Duration) error
}

func (s *Stage) Handlers() map[string]pipeline.Handler {
	return map[string]pipeline.Handler{
		pipeline.StateCleanupReady: s.hCleanup,
		pipeline.StateCleaned: func(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
			return pipeline.StateCompleted, nil
		},
	}
}

func (o Options) resolve(ctx context.Context, host string) ([]string, error) {
	if o.Resolve != nil {
		return o.Resolve(ctx, host)
	}
	return net.DefaultResolver.LookupHost(ctx, host)
}

func verification(vs []store.Verification, name string) string {
	for _, v := range vs {
		if v.CheckType == name {
			return v.Result
		}
	}
	return ""
}

func Evaluate(ctx context.Context, r *pipeline.Runner, it *store.PipelineItem, o Options) Check {
	c := Check{Item: it}
	hold := func(format string, args ...any) { c.Reasons = append(c.Reasons, fmt.Sprintf(format, args...)) }
	dummy := it.State == pipeline.StateDummyOnly
	switch {
	case it.State == pipeline.StateCleanupReady:
	case dummy && o.IncludeDummy:
	case dummy:
		hold("DUMMY_ONLY: tidak dihapus kecuali dipilih dengan --termasuk-dummy")
	case it.State == pipeline.StateCompleted || it.State == pipeline.StateCleaned:
		hold("sudah dibersihkan")
	default:
		hold("status %s belum siap cleanup", it.State)
	}
	if it.SideState != "" {
		hold("item berstatus %s", it.SideState)
	}
	steps, _ := r.Store.ListSteps(ctx, it.ID)
	for _, st := range steps {
		if st.Status == store.StepRunning {
			hold("langkah %s masih berjalan", st.Name)
		}
	}
	kind := backup.KindManifest
	if it.FinalSyncDone {
		kind = backup.FinalPrefix + backup.KindManifest
	}
	if a, err := r.Store.ValidatedArtifact(ctx, it.ID, kind); err != nil {
		hold("backup lokal tidak ditemukan")
	} else if m, err := backup.ReadManifest(a.Path); err != nil {
		hold("manifest backup tidak terbaca: %v", err)
	} else if problems := r.Backup.VerifyLocal(m); len(problems) > 0 {
		hold("backup lokal tidak valid: %s", strings.Join(problems, "; "))
	}
	vs, _ := r.Store.ListVerifications(ctx, it.ID)
	if res := verification(vs, "VERIFIKASI_HASIL"); res != "PASS" && res != "WARN" {
		hold("verifikasi tujuan belum PASS/WARN (%s)", orDash(res))
	}
	if !dummy {
		if res := verification(vs, "AKHIR_HASIL"); res == "FAIL" || res == "" {
			hold("verifikasi akhir via domain asli belum lulus (%s)", orDash(res))
		}
		if it.DestIP == "" {
			hold("IP tujuan tidak diketahui")
		} else if ips, err := o.resolve(ctx, it.SourceDomain); err != nil {
			hold("DNS %s tidak bisa dicek: %v", it.SourceDomain, err)
		} else if !contains(ips, it.DestIP) {
			hold("DNS %s masih mengarah ke %s, bukan %s", it.SourceDomain, strings.Join(ips, ", "), it.DestIP)
		}
		if o.RequireSSL && !it.SSLExcluded && verification(vs, "SSL_STATUS") != "PASS" {
			hold("SSL belum aktif (kecualikan dengan --kecualikan-ssl %s)", it.SourceDomain)
		}
		boxes, _ := r.Store.ListMailboxes(ctx, []string{it.SourceDomain})
		if len(boxes) > 0 && it.EmailState != "SELESAI" {
			hold("email domain belum selesai dipindah (%s)", orDash(it.EmailState))
		}
	}
	c.Eligible = len(c.Reasons) == 0
	return c
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func (s *Stage) hCleanup(ctx context.Context, ic *pipeline.ItemContext) (string, error) {
	r := ic.Runner()
	approval, err := gate.For(ctx, r.Store, ic.Batch.ID, gate.KindCleanup, ic.Item.SourceDomain)
	if err != nil {
		return "", pipeline.ErrIdle
	}
	chk := Evaluate(ctx, r, ic.Item, s.Opts)
	if !chk.Eligible {
		ic.Save(func(it *store.PipelineItem) { it.LastError = "cleanup ditahan: " + strings.Join(chk.Reasons, "; ") })
		return "", pipeline.ErrIdle
	}
	if err := Delete(ctx, r, ic, approval, s.Sleep); err != nil {
		return "", err
	}
	return pipeline.StateCleaned, nil
}

func Delete(ctx context.Context, r *pipeline.Runner, ic *pipeline.ItemContext, approval *gate.Approval, sleep func(context.Context, time.Duration) error) error {
	if err := approval.Require(gate.KindCleanup, ic.Item.SourceDomain); err != nil {
		return err
	}
	if _, ok := ic.ManualConfirmed(ctx, "MANUAL_CLEANUP"); ok {
		gate.Record(ctx, r.Store, approval, "cleanup.manual", ic.Item.SourceDomain, "OK", "website sumber dihapus manual oleh operator")
		return nil
	}
	api, err := ic.SourceAPI()
	if err != nil {
		return err
	}
	target := ic.Item.SourceTempDomain
	if target == "" {
		target = ic.Item.SourceDomain
	}
	sites, err := api.ListWebsites(ctx, hostinger.WebsiteFilter{Username: ic.Batch.SourceAccount})
	if err != nil {
		return err
	}
	present := false
	for _, w := range sites {
		if strings.EqualFold(w.Domain, target) {
			present = true
		}
	}
	manual := func(e error) error {
		return &pipeline.ManualError{Code: "MANUAL_CLEANUP", Instructions: fmt.Sprintf("API tidak bisa menghapus website sumber (%v).\nHapus website %s beserta databasenya di hPanel akun %s (JANGAN hapus domain/registrar), lalu jalankan: migrator confirm MANUAL_CLEANUP %s", e, target, ic.Batch.SourceAccount, ic.Item.SourceDomain)}
	}
	if present {
		_, err := api.Do(ctx, hostinger.Request{Endpoint: "hosting.websites.delete", PathParams: map[string]string{"domain": target}}, nil)
		if err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			var apiErr *hostinger.APIError
			if errors.As(err, &apiErr) && apiErr.Kind == hostinger.KindClient {
				return manual(err)
			}
			return err
		}
		gate.Record(ctx, r.Store, approval, "cleanup.delete_website", target, "OK", "website sumber "+target+" dihapus (domain/registrar tidak disentuh)")
		gone := false
		for i := 0; i < 10 && !gone; i++ {
			sites, err := api.ListWebsites(ctx, hostinger.WebsiteFilter{Username: ic.Batch.SourceAccount})
			if err != nil {
				return err
			}
			gone = true
			for _, w := range sites {
				if strings.EqualFold(w.Domain, target) {
					gone = false
				}
			}
			if !gone && sleep != nil {
				sleep(ctx, 15*time.Second)
			}
		}
		if !gone {
			return fmt.Errorf("%w: penghapusan %s masih diproses Hostinger", pipeline.ErrWait, target)
		}
	}
	dbs, err := api.ListDatabases(ctx, ic.Batch.SourceAccount)
	if err != nil {
		return err
	}
	skipped, err := pipeline.SkipSnapshot(ctx, r.Store, ic.Item)
	if err != nil {
		return err
	}
	want := deletionTargets(ic.Databases, skipped)
	for _, d := range dbs {
		if !want[d.Name] {
			continue
		}
		if _, err := api.Do(ctx, hostinger.Request{Endpoint: "hosting.databases.delete", PathParams: map[string]string{"username": ic.Batch.SourceAccount, "name": d.Name}}, nil); err != nil && !errors.Is(err, hostinger.ErrNotFound) {
			return err
		}
		gate.Record(ctx, r.Store, approval, "cleanup.delete_database", d.Name, "OK", "database sumber dihapus")
	}
	return nil
}

func deletionTargets(dbs []store.DatabaseRow, skipped []string) map[string]bool {
	want := map[string]bool{}
	for _, d := range dbs {
		if !slices.Contains(skipped, d.Name) {
			want[d.Name] = true
		}
	}
	return want
}
