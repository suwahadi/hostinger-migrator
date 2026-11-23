package cli

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/cleanup"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/report"
	"hostinger-migrator/internal/store"
)

func (a *App) cleanupOptions(includeDummy bool) cleanup.Options {
	return cleanup.Options{RequireSSL: a.cfg.Cleanup.WajibSSLAktif, IncludeDummy: includeDummy, Resolve: a.env.Resolve}
}

func newCleanupReportCmds(app *App) []*cobra.Command {
	var batchID, excludeSSL string
	var includeDummy bool
	clean := &cobra.Command{
		Use:   "cleanup",
		Short: "Hapus website dan database di akun sumber untuk item yang lolos semua gerbang (satu konfirmasi per batch)",
		Long: "Gerbang per website: backup lokal valid, verifikasi PASS/WARN, cutover selesai dan DNS mengarah ke tujuan,\n" +
			"SSL aktif (atau dikecualikan), email selesai (atau tanpa email), tidak ada langkah berjalan.\n" +
			"Domain/registrar tidak pernah dihapus. Website DUMMY_ONLY hanya dihapus dengan --termasuk-dummy.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runCleanup(cmd.Context(), batchID, includeDummy, excludeSSL)
		},
	}
	clean.Flags().StringVar(&batchID, "batch", "", "batch tertentu")
	clean.Flags().BoolVar(&includeDummy, "termasuk-dummy", false, "ikut hapus sumber website DUMMY_ONLY")
	clean.Flags().StringVar(&excludeSSL, "kecualikan-ssl", "", "domain (pisah koma) yang boleh dibersihkan walau SSL belum aktif")
	var repBatch string
	var html, js, cs bool
	rep := &cobra.Command{
		Use:   "report",
		Short: "Buat laporan migrasi (terminal, HTML, JSON, CSV)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			formats := map[string]bool{"html": html, "json": js, "csv": cs}
			if !html && !js && !cs {
				formats = map[string]bool{"html": true, "json": true, "csv": true}
			}
			return app.runReport(cmd.Context(), repBatch, formats)
		},
	}
	rep.Flags().StringVar(&repBatch, "batch", "", "batch tertentu")
	rep.Flags().BoolVar(&html, "html", false, "hanya HTML")
	rep.Flags().BoolVar(&js, "json", false, "hanya JSON")
	rep.Flags().BoolVar(&cs, "csv", false, "hanya CSV")
	return []*cobra.Command{clean, rep}
}

func (a *App) runCleanup(ctx context.Context, batchID string, includeDummy bool, excludeSSL string) error {
	excluded := map[string]bool{}
	for _, d := range strings.Split(excludeSSL, ",") {
		if d = strings.TrimSpace(d); d != "" {
			excluded[d] = true
		}
	}
	batches, err := a.store.ListBatches(ctx)
	if err != nil {
		return err
	}
	r := a.runner()
	opts := a.cleanupOptions(includeDummy)
	started := false
	for _, b := range batches {
		if batchID != "" && b.ID != batchID {
			continue
		}
		items, _ := a.store.ListItems(ctx, b.ID)
		var pass, dummies []*store.PipelineItem
		var held []cleanup.Check
		for _, it := range items {
			if excluded[it.SourceDomain] && !it.SSLExcluded {
				it, _ = a.store.UpdateItem(ctx, it.ID, "operator", "cleanup.exclude_ssl", func(x *store.PipelineItem) error { x.SSLExcluded = true; return nil })
			}
			if it.State == pipeline.StateCompleted || it.State == pipeline.StateBlocked {
				continue
			}
			if ok, _ := a.store.IsApproved(ctx, b.ID, gate.KindCleanup, it.SourceDomain); ok {
				continue
			}
			chk := cleanup.Evaluate(ctx, r, it, opts)
			switch {
			case chk.Eligible && it.State == pipeline.StateDummyOnly:
				dummies = append(dummies, it)
			case chk.Eligible:
				pass = append(pass, it)
			default:
				held = append(held, chk)
			}
		}
		a.printf("\nCLEANUP batch %s (%s/%s)\n", b.ID, b.SourceAlias, b.SourceAccount)
		if len(pass)+len(dummies) > 0 {
			a.say(toneDanger, "Lolos gerbang (website + database di akun sumber akan DIHAPUS, domain tidak dihapus):")
			for _, it := range append(pass, dummies...) {
				target := it.SourceTempDomain
				if target == "" {
					target = it.SourceDomain
				}
				a.printf("  - %s (website sumber: %s)\n", it.SourceDomain, target)
			}
		}
		if len(held) > 0 {
			a.println("Tertahan:")
			for _, h := range held {
				a.printf("  - %s: %s\n", h.Item.SourceDomain, strings.Join(h.Reasons, "; "))
			}
		}
		if len(pass)+len(dummies) == 0 {
			a.println("Tidak ada website yang lolos gerbang cleanup.")
			continue
		}
		ok, err := a.prompt.Confirm(a.color(toneDanger, "Ketik HAPUS untuk menghapus website sumber yang lolos: "), gate.Words[gate.KindCleanup])
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan.")
			continue
		}
		var domains []string
		for _, it := range append(pass, dummies...) {
			domains = append(domains, it.SourceDomain)
		}
		if err := gate.Request(ctx, a.store, b.ID, gate.KindCleanup, domains, gate.Words[gate.KindCleanup]); err != nil {
			return err
		}
		for _, it := range dummies {
			ic, err := r.Load(ctx, it)
			if err != nil {
				return err
			}
			approval, err := gate.For(ctx, a.store, b.ID, gate.KindCleanup, it.SourceDomain)
			if err == nil {
				err = cleanup.Delete(ctx, r, ic, approval, nil)
			}
			ic.Close()
			if err != nil {
				a.printError(it.SourceDomain + ": " + err.Error())
				continue
			}
			a.store.UpdateItem(ctx, it.ID, "operator", "cleanup.dummy_source", func(x *store.PipelineItem) error {
				x.LastError = "website sumber sudah dihapus (DUMMY_ONLY)"
				return nil
			})
			a.say(toneDanger, fmt.Sprintf("Sumber %s (DUMMY_ONLY) dihapus.", it.SourceDomain))
		}
		if len(pass) > 0 {
			started = true
		}
	}
	if started {
		return a.startWorker(ctx, false)
	}
	return nil
}

func (a *App) reportBuilder() *report.Builder {
	return &report.Builder{Store: a.store, BackupDir: filepath.Join(a.dataDir, "backups"), Redactor: a.redactor, Version: Version, Now: a.env.Now}
}

func (a *App) runReport(ctx context.Context, batchID string, formats map[string]bool) error {
	batches, err := a.store.ListBatches(ctx)
	if err != nil {
		return err
	}
	if len(batches) == 0 {
		a.println("Belum ada batch.")
		return nil
	}
	found := false
	for _, b := range batches {
		if batchID != "" && b.ID != batchID {
			continue
		}
		found = true
		rb := a.reportBuilder()
		rep, err := rb.Build(ctx, b.ID)
		if err != nil {
			return err
		}
		files, err := rb.Write(rep, filepath.Join(a.dataDir, "reports", b.ID), formats)
		if err != nil {
			return err
		}
		a.println()
		a.printf("%s", rep.Terminal())
		for _, f := range files {
			a.printf("File: %s\n", f)
		}
	}
	if !found {
		return needsAction(fmt.Errorf("batch %s tidak ditemukan", batchID))
	}
	return nil
}

func (a *App) autoReport(ctx context.Context, batchID string) {
	rb := a.reportBuilder()
	rep, err := rb.Build(ctx, batchID)
	if err != nil {
		return
	}
	files, err := rb.Write(rep, filepath.Join(a.dataDir, "reports", batchID), map[string]bool{"html": true, "json": true, "csv": true})
	if err == nil && a.log != nil {
		a.log.Info("laporan otomatis dibuat", "batch", batchID, "file", strings.Join(files, ", "), "waktu", time.Now().UTC().Format(time.RFC3339))
	}
}
