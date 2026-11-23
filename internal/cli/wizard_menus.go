package cli

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

var needsValue = map[string]string{
	"MANUAL_CREATE_SITE":   "Nama domain sementara website dummy yang Anda buat",
	"MANUAL_SOURCE_RENAME": "Domain sementara yang sekarang dipakai website sumber",
}

func (a *App) manualMenu(ctx context.Context) error {
	next := func(err error) error {
		if err := a.finish(err, true); err != nil {
			return err
		}
		a.clearScreen()
		return nil
	}
	for {
		tasks, err := a.store.ListManualTasks(ctx, true)
		if err != nil {
			return err
		}
		a.println()
		a.say(toneTitle, "TINDAKAN MANUAL")
		if len(tasks) == 0 {
			a.println("Tidak ada tindakan manual yang menunggu.")
			return a.pause()
		}
		for i, t := range tasks {
			a.printf("\n[%d] %s · %s (batch %s) · sejak %s\n", i+1, t.Code, t.Domain, t.BatchID, t.CreatedAt)
			for _, line := range strings.Split(t.Instructions, "\n") {
				a.printf("    %s\n", line)
			}
		}
		a.println()
		a.println("Ketik nomor tindakan yang SUDAH Anda kerjakan untuk dikonfirmasi,")
		a.println("R = tampilkan password yang dibutuhkan, ENTER = kembali.")
		v, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return nil
		}
		if strings.EqualFold(v, "R") {
			a.showSecretsFor(ctx, tasks)
			if err := next(nil); err != nil {
				return err
			}
			continue
		}
		n, convErr := strconv.Atoi(v)
		if convErr != nil || n < 1 || n > len(tasks) {
			a.say(toneWarn, "Pilihan tidak dikenal.")
			if err := next(nil); err != nil {
				return err
			}
			continue
		}
		t := tasks[n-1]
		value := ""
		if label, ok := needsValue[t.Code]; ok {
			value, err = a.askDefault(label, "")
			if err != nil {
				return err
			}
		}
		ok, err := a.prompt.Confirm(a.color(toneOK, fmt.Sprintf("Yakin %s untuk %s sudah selesai dikerjakan? (y/T): ", t.Code, t.Domain)), "y")
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan.")
			if err := next(nil); err != nil {
				return err
			}
			continue
		}
		it, err := pipeline.Confirm(ctx, a.store, t.Code, t.Domain, t.BatchID, value)
		if err != nil {
			if err := next(err); err != nil {
				return err
			}
			continue
		}
		a.say(toneOK, fmt.Sprintf("%s untuk %s dikonfirmasi (status %s).", t.Code, t.Domain, pipeline.ItemLabel(it)))
		if err := a.startWorker(ctx, false); err != nil {
			return err
		}
		if err := next(nil); err != nil {
			return err
		}
	}
}

func (a *App) showSecretsFor(ctx context.Context, tasks []*store.ManualTask) {
	shown := 0
	for _, t := range tasks {
		it, err := a.store.GetItem(ctx, t.ItemID)
		if err != nil {
			continue
		}
		b, _, err := a.store.GetBatch(ctx, it.BatchID)
		if err != nil {
			continue
		}
		switch t.Code {
		case "MANUAL_CREATE_DB":
			for _, d := range it.DestDBs {
				if pw, err := a.secrets.Get(secrets.KindDBPassword, b.DestAccount+":"+d.Name); err == nil && !d.Created {
					fmt.Fprintf(a.env.Stdout, "  Password database %s: %s\n", d.Name, pw)
					shown++
				}
			}
		case "MANUAL_EMAIL_PASSWORD", "MANUAL_EMAIL_CREATE":
			boxes, _ := a.store.ListMailboxes(ctx, []string{it.SourceDomain})
			for _, m := range boxes {
				if pw, err := a.secrets.Get(secrets.KindMailboxPassword, m.Address); err == nil {
					fmt.Fprintf(a.env.Stdout, "  Password %s: %s\n", m.Address, pw)
					shown++
				}
			}
		}
	}
	if shown == 0 {
		a.println("Tidak ada password yang dibutuhkan untuk tindakan di atas.")
		return
	}
	a.store.AppendAudit(ctx, nil, "operator", "manual.secrets_shown", "", "OK", fmt.Sprintf("%d rahasia ditampilkan", shown))
}

func (a *App) emailMenu(ctx context.Context) error {
	for {
		a.println()
		a.say(toneTitle, "PINDAHKAN EMAIL")
		a.println("1. Setujui pemindahan email (website yang sudah cutover)")
		a.println("2. Tampilkan password mailbox baru")
		a.println("0. Kembali")
		v, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		v = strings.TrimSpace(v)
		if v == "0" || v == "" {
			return nil
		}
		a.clearScreen()
		var actionErr error
		switch v {
		case "1":
			actionErr = a.approveEmail(ctx, "")
		case "2":
			actionErr = a.showMailPasswords(ctx, "", "")
		default:
			a.say(toneWarn, "Pilihan tidak dikenal.")
		}
		if err := a.finish(actionErr, true); err != nil {
			return err
		}
		a.clearScreen()
	}
}

func (a *App) cleanupWizard(ctx context.Context) error {
	v, err := a.prompt.Line("Ikut hapus website sumber yang berstatus DUMMY_ONLY? (y/T): ")
	if err != nil {
		return err
	}
	includeDummy := strings.EqualFold(strings.TrimSpace(v), "y")
	excl, err := a.prompt.Line("Domain yang boleh dibersihkan walau SSL belum aktif (pisah koma, ENTER = tidak ada): ")
	if err != nil {
		return err
	}
	return a.runCleanup(ctx, "", includeDummy, strings.TrimSpace(excl))
}

func (a *App) workerMenu(ctx context.Context) error {
	for {
		a.println()
		a.say(toneTitle, "KENDALI WORKER & PEMULIHAN")
		a.println("1. Jalankan worker (start)")
		a.println("2. Lanjutkan setelah gangguan (resume)")
		a.println("3. Hentikan worker (stop)")
		a.println("4. Ulangi website yang gagal (retry)")
		a.println("5. Rollback cutover satu website")
		a.println("6. Aktifkan autostart saat login Windows")
		a.println("7. Matikan autostart")
		a.println("0. Kembali")
		v, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		v = strings.TrimSpace(v)
		if v == "0" || v == "" {
			return nil
		}
		a.clearScreen()
		var actionErr error
		switch v {
		case "1":
			actionErr = a.startWorker(ctx, false)
		case "2":
			actionErr = a.startWorker(ctx, true)
		case "3":
			actionErr = a.stopWorker(ctx)
		case "4":
			b, err := a.prompt.Line("Ikut ulangi website BLOCKED (setelah penyebabnya diperbaiki)? (y/T): ")
			if err != nil {
				return err
			}
			done, err := pipeline.Retry(ctx, a.store, "", "", strings.EqualFold(strings.TrimSpace(b), "y"))
			if err != nil {
				actionErr = err
				break
			}
			if len(done) == 0 {
				a.println("Tidak ada website yang perlu diulang.")
				break
			}
			a.printf("Diulang: %s\n", strings.Join(done, ", "))
			actionErr = a.startWorker(ctx, false)
		case "5":
			domain, err := a.pickRollback(ctx)
			if err != nil {
				return err
			}
			if domain != "" {
				actionErr = a.runRollback(ctx, domain, "")
			}
		case "6":
			actionErr = a.autostart("on")
		case "7":
			actionErr = a.autostart("off")
		default:
			a.say(toneWarn, "Pilihan tidak dikenal.")
		}
		if err := a.finish(actionErr, true); err != nil {
			return err
		}
		a.clearScreen()
	}
}

var rollbackStates = map[string]bool{
	pipeline.StateCutoverRunning: true, pipeline.StateCutoverDone: true, pipeline.StateSSLActive: true, pipeline.StateSSLPending: true,
	pipeline.StateNoEmail: true, pipeline.StateCleanupReady: true, pipeline.StateEmailRunning: true,
}

func (a *App) pickRollback(ctx context.Context) (string, error) {
	items, err := a.store.ListItems(ctx, "")
	if err != nil {
		return "", err
	}
	var options []*store.PipelineItem
	for _, it := range items {
		if rollbackStates[it.State] {
			options = append(options, it)
		}
	}
	if len(options) == 0 {
		a.println("Tidak ada website yang bisa di-rollback (hanya yang sudah memulai cutover dan belum cleanup).")
		return "", nil
	}
	for i, it := range options {
		a.printf("%d. %s (%s)\n", i+1, it.SourceDomain, pipeline.ItemLabel(it))
	}
	v, err := a.prompt.Line("Pilih website: ")
	if err != nil {
		return "", err
	}
	n, convErr := strconv.Atoi(strings.TrimSpace(v))
	if convErr != nil || n < 1 || n > len(options) {
		if strings.TrimSpace(v) != "" {
			a.println("Pilihan tidak dikenal.")
		}
		return "", nil
	}
	return options[n-1].SourceDomain, nil
}
