package cli

import (
	"context"
	"fmt"
	"strings"
)

type menuEntry struct {
	key   string
	label string
}

var mainMenu = []menuEntry{
	{"1", "Kelola API token"},
	{"2", "Siapkan SSH key & audit"},
	{"3", "Scan akun (inventaris)"},
	{"4", "Buat rencana & mulai migrasi"},
	{"5", "Status migrasi"},
	{"6", "Tindakan manual (lihat & konfirmasi)"},
	{"7", "Cutover domain"},
	{"8", "Pindahkan email"},
	{"9", "Bersihkan akun sumber (cleanup)"},
	{"10", "Laporan"},
	{"11", "Kendali worker & pemulihan"},
}

const bannerWidth = 34

func center(s string) string {
	pad := (bannerWidth - len([]rune(s))) / 2
	if pad < 0 {
		pad = 0
	}
	return strings.Repeat(" ", pad) + s
}

func (a *App) banner() {
	line := strings.Repeat("=", bannerWidth)
	a.println(line)
	a.say(toneTitle, center("INFORMASI PROGRAM"))
	a.println(line)
	a.println()
	a.println(" Nama Program : Hostinger Migrator")
	a.println(" Pembuat      : Suwahadi")
	a.println(" Versi        : " + Version)
	a.println(" Platform     : Windows 10/11 64bit")
	a.println()
	a.println(line)
	a.println(center("Dibuat oleh: Suwahadi (c) 2026"))
	a.println(line)
}

func (a *App) runWizard(ctx context.Context) error {
	a.clearScreen()
	a.banner()
	if a.interactive {
		a.printStartupWarning()
	}
	for {
		a.println()
		a.say(toneTitle, "MENU UTAMA — HOSTINGER MIGRATION MANAGER")
		for _, m := range mainMenu {
			a.printf("%2s. %s\n", m.key, m.label)
		}
		a.println(" 0. Keluar")
		choice, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		choice = strings.TrimSpace(choice)
		if choice == "0" {
			a.println("Sampai jumpa. Worker yang sedang berjalan tetap bekerja di background.")
			return nil
		}
		a.clearScreen()
		var actionErr error
		pause := true
		switch choice {
		case "1":
			pause = false
			actionErr = a.tokenMenu(ctx)
		case "2":
			pause = false
			actionErr = a.sshMenu(ctx)
		case "3":
			actionErr = a.runInventory(ctx, "", a.askRefresh())
		case "4":
			actionErr = a.runPlan(ctx, planOptions{})
		case "5":
			actionErr = a.showStatus(ctx, "", "")
		case "6":
			pause = false
			actionErr = a.manualMenu(ctx)
		case "7":
			actionErr = a.runCutover(ctx, "", "")
		case "8":
			pause = false
			actionErr = a.emailMenu(ctx)
		case "9":
			actionErr = a.cleanupWizard(ctx)
		case "10":
			actionErr = a.runReport(ctx, "", map[string]bool{"html": true, "json": true, "csv": true})
		case "11":
			pause = false
			actionErr = a.workerMenu(ctx)
		default:
			a.say(toneWarn, "Pilihan tidak dikenal.")
		}
		if err := a.finish(actionErr, pause); err != nil {
			return err
		}
		a.clearScreen()
	}
}

func (a *App) tokenMenu(ctx context.Context) error {
	for {
		a.println()
		a.say(toneTitle, "KELOLA API TOKEN")
		a.println("1. Tambah token")
		a.println("2. Daftar token")
		a.println("3. Tes token")
		a.println("4. Hapus token (lokal saja)")
		a.println("0. Kembali")
		choice, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		choice = strings.TrimSpace(choice)
		if choice == "0" {
			return nil
		}
		a.clearScreen()
		var actionErr error
		switch choice {
		case "1":
			actionErr = a.accountAdd(ctx, "", false)
		case "2":
			actionErr = a.accountList(ctx)
		case "3":
			alias, err := a.pickAlias(ctx, "Alias yang diuji: ")
			if err != nil {
				return err
			}
			if alias != "" {
				actionErr = a.accountTest(ctx, alias)
			}
		case "4":
			alias, err := a.pickAlias(ctx, "Alias yang dihapus: ")
			if err != nil {
				return err
			}
			if alias != "" {
				actionErr = a.accountRemove(ctx, alias, false)
			}
		default:
			a.say(toneWarn, "Pilihan tidak dikenal.")
		}
		if err := a.finish(actionErr, true); err != nil {
			return err
		}
		a.clearScreen()
	}
}

func (a *App) pickAlias(ctx context.Context, prompt string) (string, error) {
	items, err := a.accounts().List(ctx)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		a.println("Belum ada token terdaftar.")
		return "", nil
	}
	for i, it := range items {
		a.printf("%d. %s (%s)\n", i+1, it.Credential.Alias, it.Credential.LastStatus)
	}
	v, err := a.prompt.Line(prompt)
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	for i, it := range items {
		if v == fmt.Sprint(i+1) || v == it.Credential.Alias {
			return it.Credential.Alias, nil
		}
	}
	if v != "" {
		a.println("Alias tidak ditemukan.")
	}
	return "", nil
}

func (a *App) sshMenu(ctx context.Context) error {
	for {
		a.println()
		a.say(toneTitle, "SIAPKAN SSH KEY & AUDIT")
		a.println("1. Setup SSH semua akun yang belum siap")
		a.println("2. Setup SSH satu akun")
		a.println("3. Tes SSH semua akun")
		a.println("4. Audit kapabilitas (read-only)")
		a.println("5. Hapus password SSH cadangan")
		a.println("0. Kembali")
		choice, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		choice = strings.TrimSpace(choice)
		if choice == "0" {
			return nil
		}
		a.clearScreen()
		var actionErr error
		switch choice {
		case "1":
			actionErr = a.sshSetup(ctx, "", false)
		case "2":
			id, err := a.pickAccount(ctx)
			if err != nil {
				return err
			}
			if id != "" {
				actionErr = a.sshSetup(ctx, id, false)
			}
		case "3":
			actionErr = a.sshTest(ctx, "")
		case "4":
			actionErr = a.runAudit(ctx, "")
		case "5":
			id, err := a.pickAccount(ctx)
			if err != nil {
				return err
			}
			if id != "" {
				actionErr = a.sshRemovePassword(ctx, id)
			}
		default:
			a.say(toneWarn, "Pilihan tidak dikenal.")
		}
		if err := a.finish(actionErr, true); err != nil {
			return err
		}
		a.clearScreen()
	}
}

func (a *App) pickAccount(ctx context.Context) (string, error) {
	all, err := a.store.ListSSH(ctx)
	if err != nil {
		return "", err
	}
	if len(all) == 0 {
		a.println("Belum ada akun hosting. Tambahkan token dulu di menu 1.")
		return "", nil
	}
	for i, info := range all {
		a.printf("%d. %s (%s, SSH %s)\n", i+1, info.AccountID, PlanLabel(info.Plan), info.Status)
	}
	v, err := a.prompt.Line("Pilih akun: ")
	if err != nil {
		return "", err
	}
	v = strings.TrimSpace(v)
	for i, info := range all {
		if v == fmt.Sprint(i+1) || v == info.AccountID {
			return info.AccountID, nil
		}
	}
	if v != "" {
		a.println("Akun tidak ditemukan.")
	}
	return "", nil
}

func (a *App) askRefresh() bool {
	v, err := a.prompt.Line("Pindai ulang dari server? (y/T): ")
	if err != nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(v), "y")
}
