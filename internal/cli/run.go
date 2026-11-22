package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/verify"
	"hostinger-migrator/internal/worker"
)

func (a *App) runner() *pipeline.Runner {
	d := pipeline.Deps{
		Store:     a.store,
		Secrets:   a.secrets,
		SSH:       a.sshService(),
		NewClient: a.tokenClient,
		Backup:    &backup.Engine{Store: a.store, BackupDir: filepath.Join(a.dataDir, "backups"), Redactor: a.redactor, Log: a.log, Version: Version},
		Verifier: &verify.Verifier{
			Timeout:   time.Duration(a.cfg.Verifikasi.TimeoutHTTPDetik) * time.Second,
			MinAssets: a.cfg.Verifikasi.MinAssetDicek,
			SourceTitle: func(ctx context.Context, domain string) (string, error) {
				return verify.FetchTitle(ctx, domain, 15*time.Second)
			},
		},
		Redactor: a.redactor,
		Log:      a.log,
		RetryMax: a.cfg.API.RetryMaks,
		Parallel: a.cfg.Worker.WebsiteParalel,
		Stages:   a.stages(),
	}
	if a.env.RunnerHook != nil {
		a.env.RunnerHook(&d)
	}
	d.OnBatchFinished = a.onBatchFinished
	return pipeline.NewRunner(d)
}

func newWorkerCmds(app *App) []*cobra.Command {
	start := &cobra.Command{
		Use:   "start",
		Short: "Jalankan worker migrasi di background (terminal boleh ditutup)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.startWorker(cmd.Context(), false)
		},
	}
	resume := &cobra.Command{
		Use:   "resume",
		Short: "Lanjutkan migrasi setelah gangguan (langkah terputus diperiksa ulang)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.startWorker(cmd.Context(), true)
		},
	}
	stop := &cobra.Command{
		Use:   "stop",
		Short: "Hentikan worker dengan rapi",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.stopWorker(cmd.Context())
		},
	}
	var batchID, item string
	status := &cobra.Command{
		Use:   "status",
		Short: "Tampilkan status worker, batch, dan website",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.showStatus(cmd.Context(), batchID, item)
		},
	}
	status.Flags().StringVar(&batchID, "batch", "", "batch tertentu")
	status.Flags().StringVar(&item, "item", "", "detail satu website (domain)")
	var failed bool
	var retryItem, retryBatch string
	var withBlocked bool
	retry := &cobra.Command{
		Use:   "retry",
		Short: "Ulangi website yang FAILED (atau satu website)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !failed && retryItem == "" {
				return errors.New("gunakan --failed atau --item <domain>")
			}
			done, err := pipeline.Retry(cmd.Context(), app.store, retryBatch, retryItem, withBlocked)
			if err != nil {
				return err
			}
			if len(done) == 0 {
				app.println("Tidak ada website yang perlu diulang.")
				return nil
			}
			app.printf("Diulang: %s\n", strings.Join(done, ", "))
			return app.startWorker(cmd.Context(), false)
		},
	}
	retry.Flags().BoolVar(&failed, "failed", false, "semua website berstatus FAILED")
	retry.Flags().StringVar(&retryItem, "item", "", "satu website (domain)")
	retry.Flags().StringVar(&retryBatch, "batch", "", "batch tertentu")
	retry.Flags().BoolVar(&withBlocked, "termasuk-blocked", false, "ikut ulangi website BLOCKED (setelah penyebabnya diperbaiki)")
	var showSecrets bool
	pending := &cobra.Command{
		Use:   "pending",
		Short: "Daftar tindakan manual yang menunggu operator",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.showPending(cmd.Context(), showSecrets)
		},
	}
	pending.Flags().BoolVar(&showSecrets, "tampilkan-rahasia", false, "tampilkan password yang dibutuhkan untuk langkah manual (hanya di terminal)")
	var value, confirmBatch string
	confirm := &cobra.Command{
		Use:     "confirm <KODE> <domain>",
		Short:   "Konfirmasi tindakan manual sudah dikerjakan, lalu lanjutkan worker",
		Example: "  migrator confirm MANUAL_BACKUP domainanda.com\n  migrator confirm MANUAL_CREATE_SITE domainanda.com --nilai abc.hostingersite.com",
		Args:    exactArgs(2, "<KODE> <domain>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			it, err := pipeline.Confirm(cmd.Context(), app.store, args[0], args[1], confirmBatch, value)
			if err != nil {
				return needsAction(err)
			}
			app.printf("%s untuk %s dikonfirmasi (status %s).\n", strings.ToUpper(args[0]), args[1], pipeline.ItemLabel(it))
			return app.startWorker(cmd.Context(), false)
		},
	}
	confirm.Flags().StringVar(&value, "nilai", "", "nilai yang diminta langkah manual (mis. nama domain dummy)")
	confirm.Flags().StringVar(&confirmBatch, "batch", "", "batch tertentu")
	autostart := &cobra.Command{
		Use:   "autostart <on|off>",
		Short: "Daftarkan/hapus worker di Windows Task Scheduler saat login",
		Args:  exactArgs(1, "<on|off>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.autostart(args[0])
		},
	}
	var once bool
	work := &cobra.Command{
		Use:    "worker",
		Short:  "Proses worker (dipanggil otomatis oleh start)",
		Hidden: true,
		Args:   noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			w := &worker.Worker{Store: app.store, Runner: app.runner(), Log: app.log}
			if once {
				w.Interval = time.Millisecond
			}
			err := w.Run(cmd.Context())
			if errors.Is(err, worker.ErrAlreadyRunning) {
				return needsAction(err)
			}
			return err
		},
	}
	work.Flags().BoolVar(&once, "cepat", false, "tanpa jeda antar siklus")
	return []*cobra.Command{start, resume, stop, status, retry, pending, confirm, autostart, work}
}

func (a *App) absConfig() string {
	p, err := filepath.Abs(a.configPath)
	if err != nil {
		return a.configPath
	}
	return p
}

func (a *App) startWorker(ctx context.Context, resume bool) error {
	if l, alive := worker.Alive(ctx, a.store); alive {
		a.printf("Worker sudah berjalan (PID %d). Pantau dengan: migrator status\n", l.PID)
		return nil
	}
	if resume {
		if n, _ := a.store.ResetRunningSteps(ctx); n > 0 {
			a.printf("%d langkah yang terputus akan diperiksa ulang.\n", n)
		}
	}
	if a.env.SpawnWorker != nil {
		return a.env.SpawnWorker(ctx, a)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	pid, err := worker.Spawn(exe, []string{"worker", "--config", a.absConfig()}, filepath.Join(a.dataDir, "logs", "worker-output.log"))
	if err != nil {
		return fmt.Errorf("gagal menjalankan worker: %w", err)
	}
	a.printf("Worker berjalan di background (PID %d). Terminal boleh ditutup.\n", pid)
	a.println("Pantau: migrator status · Tindakan manual: migrator pending · Hentikan: migrator stop")
	return nil
}

func (a *App) RunWorkerInProcess(ctx context.Context) error {
	w := &worker.Worker{Store: a.store, Runner: a.runner(), Log: a.log, Interval: time.Millisecond}
	return w.Run(ctx)
}

func (a *App) stopWorker(ctx context.Context) error {
	l, alive := worker.Alive(ctx, a.store)
	if !alive {
		a.println("Tidak ada worker yang berjalan.")
		return nil
	}
	if _, err := a.store.RequestWorkerStop(ctx); err != nil {
		return err
	}
	a.store.AppendAudit(ctx, nil, "operator", "worker.stop_request", fmt.Sprint(l.PID), "OK", "")
	a.printf("Permintaan berhenti dikirim ke worker PID %d. Worker berhenti setelah langkah yang sedang berjalan; langkah besar diulang saat start/resume.\n", l.PID)
	return nil
}

func (a *App) showStatus(ctx context.Context, batchID, domain string) error {
	if l, alive := worker.Alive(ctx, a.store); alive {
		state := "berjalan"
		if l.StopRequested {
			state = "sedang berhenti"
		}
		a.printf("Worker: %s (PID %d, sejak %s)\n", state, l.PID, l.StartedAt)
	} else {
		a.println("Worker: tidak berjalan (jalankan 'migrator start')")
	}
	if domain != "" {
		return a.showItem(ctx, batchID, domain)
	}
	batches, err := a.store.ListBatches(ctx)
	if err != nil {
		return err
	}
	if len(batches) == 0 {
		a.println("Belum ada batch. Buat dengan: migrator plan")
		return nil
	}
	for _, b := range batches {
		if batchID != "" && b.ID != batchID {
			continue
		}
		items, _ := a.store.ListItems(ctx, b.ID)
		a.printf("\nBatch %s · %s · %s/%s → %s/%s\n", b.ID, b.Status, b.SourceAlias, b.SourceAccount, b.DestAlias, b.DestAccount)
		tw := tabwriter.NewWriter(a.out(), 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "  WEBSITE\tSTATUS\tDUMMY\tCATATAN")
		for _, it := range items {
			note := it.LastError
			if it.ManualCode != "" {
				note = "menunggu " + it.ManualCode + " (lihat migrator pending)"
			}
			dummy := it.DummyDomain
			if dummy == "" {
				dummy = "-"
			}
			fmt.Fprintf(tw, "  %s\t%s\t%s\t%s\n", it.SourceDomain, pipeline.ItemLabel(it), dummy, a.redactor.String(note))
		}
		tw.Flush()
	}
	return nil
}

func (a *App) showItem(ctx context.Context, batchID, domain string) error {
	it, err := a.store.FindItem(ctx, domain, batchID)
	if err != nil {
		return needsAction(fmt.Errorf("website %s tidak ada di batch mana pun", domain))
	}
	a.printf("\n%s (batch %s)\nStatus      : %s\nDummy       : %s\nFolder tujuan: %s\nIP tujuan   : %s\n", it.SourceDomain, it.BatchID, pipeline.ItemLabel(it), it.DummyDomain, it.DestRootPath, it.DestIP)
	if it.LastError != "" {
		a.printf("Catatan     : %s\n", a.redactor.String(it.LastError))
	}
	for _, d := range it.DestDBs {
		a.printf("Database    : %s → %s (dibuat %v, diimport %v)\n", d.Source, d.Name, d.Created, d.Imported)
	}
	steps, _ := a.store.ListSteps(ctx, it.ID)
	if len(steps) > 0 {
		a.println("Langkah:")
		for _, s := range steps {
			line := fmt.Sprintf("  %-14s %-8s percobaan %d", s.Name, s.Status, s.Attempts)
			if s.Error != "" {
				line += " · " + a.redactor.String(s.Error)
			}
			a.println(line)
		}
	}
	vs, _ := a.store.ListVerifications(ctx, it.ID)
	if len(vs) > 0 {
		a.println("Verifikasi:")
		for _, v := range vs {
			a.printf("  %-28s %-4s %s\n", v.CheckType, v.Result, a.redactor.String(v.Detail))
		}
	}
	arts, _ := a.store.ListArtifacts(ctx, it.ID)
	if len(arts) > 0 {
		a.println("Backup lokal:")
		for _, ar := range arts {
			a.printf("  %-24s %s (%s)\n", ar.Kind, ar.Path, sizeLabel(ar.Size))
		}
	}
	return nil
}

func (a *App) showPending(ctx context.Context, showSecrets bool) error {
	tasks, err := a.store.ListManualTasks(ctx, true)
	if err != nil {
		return err
	}
	if len(tasks) == 0 {
		a.println("Tidak ada tindakan manual yang menunggu.")
		return nil
	}
	a.printf("%d tindakan manual menunggu:\n", len(tasks))
	for i, t := range tasks {
		a.printf("\n[%d] %s · %s (batch %s) · sejak %s\n", i+1, t.Code, t.Domain, t.BatchID, t.CreatedAt)
		for _, line := range strings.Split(t.Instructions, "\n") {
			a.printf("    %s\n", line)
		}
		if showSecrets && t.Code == "MANUAL_CREATE_DB" {
			it, err := a.store.GetItem(ctx, t.ItemID)
			if err == nil {
				b, _, _ := a.store.GetBatch(ctx, it.BatchID)
				for _, d := range it.DestDBs {
					if d.Created {
						continue
					}
					if pw, err := a.secrets.Get(secrets.KindDBPassword, b.DestAccount+":"+d.Name); err == nil {
						fmt.Fprintf(a.env.Stdout, "    Password %s: %s\n", d.Name, pw)
					}
				}
			}
		}
	}
	a.println("\nSetelah dikerjakan: migrator confirm <KODE> <domain>")
	return needsAction(fmt.Errorf("%d tindakan manual menunggu", len(tasks)))
}

func (a *App) autostart(mode string) error {
	on := strings.EqualFold(mode, "on")
	if !on && !strings.EqualFold(mode, "off") {
		return errors.New("gunakan 'on' atau 'off'")
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	args, err := worker.AutostartCommand(on, exe, a.absConfig())
	if err != nil {
		return err
	}
	out, err := exec.Command(args[0], args[1:]...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("schtasks gagal: %v: %s", err, strings.TrimSpace(string(out)))
	}
	if on {
		a.printf("Autostart aktif: worker dilanjutkan otomatis saat login Windows (tugas %q).\n", worker.TaskName)
	} else {
		a.println("Autostart dimatikan.")
	}
	a.store.AppendAudit(context.Background(), nil, "operator", "autostart."+strings.ToLower(mode), worker.TaskName, "OK", "")
	return nil
}

func (a *App) stages() []pipeline.Stage {
	return nil
}

func (a *App) onBatchFinished(ctx context.Context, batchID string) {
	if a.log != nil {
		a.log.Info("batch selesai", "batch", batchID)
	}
}
