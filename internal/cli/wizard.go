package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type menuEntry struct {
	key   string
	label string
	phase int
}

var mainMenu = []menuEntry{
	{"1", "Kelola API token", 1},
	{"2", "Siapkan SSH key", 2},
	{"3", "Scan akun (inventaris)", 3},
	{"4", "Buat rencana & mulai migrasi", 4},
	{"5", "Status migrasi", 4},
	{"6", "Tindakan manual tertunda", 4},
	{"7", "Laporan", 7},
}

const implementedPhase = 1

func (a *App) runWizard(ctx context.Context) error {
	for {
		a.println()
		a.println("HOSTINGER MIGRATION MANAGER")
		for _, m := range mainMenu {
			suffix := ""
			if m.phase > implementedPhase {
				suffix = "  (belum tersedia)"
			}
			a.printf("%s. %s%s\n", m.key, m.label, suffix)
		}
		a.println("0. Keluar")
		choice, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		choice = strings.TrimSpace(choice)
		switch choice {
		case "0":
			a.println("Sampai jumpa.")
			return nil
		case "1":
			if err := a.tokenMenu(ctx); err != nil {
				return err
			}
		default:
			entry, ok := findMenu(choice)
			if !ok {
				a.println("Pilihan tidak dikenal.")
				continue
			}
			a.printf("Menu %q belum tersedia pada versi ini (dikerjakan di Fase %d).\n", entry.label, entry.phase)
		}
	}
}

func findMenu(key string) (menuEntry, bool) {
	for _, m := range mainMenu {
		if m.key == key {
			return m, true
		}
	}
	return menuEntry{}, false
}

func (a *App) tokenMenu(ctx context.Context) error {
	for {
		a.println()
		a.println("KELOLA API TOKEN")
		a.println("1. Tambah token")
		a.println("2. Daftar token")
		a.println("3. Tes token")
		a.println("4. Hapus token (lokal saja)")
		a.println("0. Kembali")
		choice, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return err
		}
		var actionErr error
		switch strings.TrimSpace(choice) {
		case "0":
			return nil
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
			a.println("Pilihan tidak dikenal.")
		}
		if actionErr != nil {
			if errors.Is(actionErr, ErrInputClosed) {
				return actionErr
			}
			fmt.Fprintln(a.env.Stderr, "ERROR: "+a.redactor.String(actionErr.Error()))
		}
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
