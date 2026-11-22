package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/capability"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/inventory"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

func newInventoryCmd(app *App) *cobra.Command {
	var refresh bool
	cmd := &cobra.Command{
		Use:   "inventory [alias]",
		Short: "Inventaris website, root path, tipe, ukuran, database, mailbox (read-only)",
		Long:  "Menampilkan inventaris setiap akun hosting di balik token. Bila belum pernah dipindai, atau dengan --refresh,\ntool memindai ulang lewat API (read-only) dan SSH (perintah baca saja). Inventaris tidak pernah mengubah apa pun.",
		Args:  maxArgs(1, "[alias]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := ""
			if len(args) == 1 {
				alias = args[0]
			}
			return app.runInventory(cmd.Context(), alias, refresh)
		},
	}
	cmd.Flags().BoolVar(&refresh, "refresh", false, "pindai ulang walaupun inventaris sudah ada")
	return cmd
}

func (a *App) tokenClient(alias string) (*hostinger.Client, error) {
	token, err := a.secrets.Token(alias)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil, fmt.Errorf("%w untuk alias %s", account.ErrTokenMissing, alias)
	}
	if err != nil {
		return nil, err
	}
	return a.newClient(alias, token)
}

func (a *App) scanner() *inventory.Scanner {
	return &inventory.Scanner{
		Store:     a.store,
		NewClient: a.tokenClient,
		SSH:       a.sshService(),
		DataDir:   a.dataDir,
		Redactor:  a.redactor,
		Log:       a.log,
		LookupNS:  a.lookupNS,
	}
}

func (a *App) aliases(ctx context.Context, alias string) ([]string, error) {
	creds, err := a.store.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, c := range creds {
		if alias == "" || c.Alias == alias {
			out = append(out, c.Alias)
		}
	}
	if len(out) == 0 {
		if alias != "" {
			return nil, needsAction(fmt.Errorf("%w: %s", account.ErrAliasNotFound, alias))
		}
		return nil, needsAction(errors.New("belum ada token terdaftar; jalankan 'migrator account add'"))
	}
	return out, nil
}

func (a *App) ensureInventory(ctx context.Context, alias string, refresh bool) error {
	accs, err := a.store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return err
	}
	need := refresh
	for _, acc := range accs {
		if _, err := a.store.GetInventoryRun(ctx, acc.ID, alias); errors.Is(err, store.ErrNotFound) {
			need = true
		}
	}
	if !need {
		return nil
	}
	a.printf("Memindai inventaris token %s (read-only)...\n", alias)
	_, err = a.scanner().ScanAlias(ctx, alias)
	if errors.Is(err, inventory.ErrTokenRejected) {
		a.store.WithTxUpdateCredential(ctx, alias, store.CredentialRejected, a.redactor.String(err.Error()))
		return needsAction(err)
	}
	if errors.Is(err, account.ErrTokenMissing) {
		return needsAction(err)
	}
	return err
}

func (a *App) runInventory(ctx context.Context, alias string, refresh bool) error {
	aliases, err := a.aliases(ctx, alias)
	if err != nil {
		return err
	}
	var failed []error
	for _, al := range aliases {
		if err := a.ensureInventory(ctx, al, refresh); err != nil {
			var ee *exitError
			if errors.As(err, &ee) {
				fmt.Fprintln(a.env.Stderr, "ERROR: "+a.redactor.String(err.Error()))
				failed = append(failed, err)
				continue
			}
			return err
		}
		a.printf("\nINVENTARIS — token %s\n", al)
		if err := a.printAliasInventory(ctx, al, true); err != nil {
			return err
		}
	}
	a.println()
	a.println("Ukuran \"?\" berarti tidak diketahui (SSH belum siap). Tipe diberi persen bila keyakinan deteksi di bawah 70%.")
	if len(failed) > 0 {
		return needsAction(errors.Join(failed...))
	}
	return nil
}

type listedAccount struct {
	Account store.HostingAccount
	Units   []store.Website
}

func (a *App) printAliasInventory(ctx context.Context, alias string, detail bool) error {
	listed, err := a.listInventory(ctx, alias)
	if err != nil {
		return err
	}
	for i, la := range listed {
		a.printAccountInventory(ctx, i+1, la, detail)
	}
	return nil
}

func (a *App) listInventory(ctx context.Context, alias string) ([]listedAccount, error) {
	accs, err := a.store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return nil, err
	}
	var out []listedAccount
	for _, acc := range accs {
		ws, err := a.store.ListWebsites(ctx, acc.ID)
		if err != nil {
			return nil, err
		}
		la := listedAccount{Account: acc}
		for _, w := range ws {
			if w.IsUnit() {
				la.Units = append(la.Units, w)
			}
		}
		out = append(out, la)
	}
	return out, nil
}

func sizeLabel(n int64) string {
	if n < 0 {
		return "?"
	}
	return capability.HumanBytes(n)
}

func typeLabel(w store.Website) string {
	t := w.SiteType
	if t == "" {
		t = "?"
	}
	if w.TypeConfidence > 0 && w.TypeConfidence < 0.7 {
		t = fmt.Sprintf("%s (yakin %d%%)", t, int(w.TypeConfidence*100+0.5))
	}
	return t
}

func flags(w store.Website) string {
	var f []string
	switch w.DomainStatus {
	case store.DomainExpired:
		f = append(f, "EXPIRED")
	case store.DomainDisabled:
		f = append(f, "DISABLED")
	case store.DomainUnknown:
		f = append(f, "STATUS DOMAIN ?")
	}
	if w.NSExternal != nil && *w.NSExternal {
		f = append(f, "NS EKSTERNAL")
	}
	if len(f) == 0 {
		return ""
	}
	return "  [" + strings.Join(f, "] [") + "]"
}

func (a *App) printAccountInventory(ctx context.Context, n int, la listedAccount, detail bool) {
	acc := la.Account
	ssh, _ := a.store.GetSSH(ctx, acc.ID)
	sshStatus := store.SSHNotConfigured
	if ssh != nil {
		sshStatus = ssh.Status
	}
	a.printf("[%d] %s  (%s) · SSH %s\n", n, acc.ID, PlanLabel(acc.Plan), sshStatus)
	if len(la.Units) == 0 {
		a.println("    (tidak ada website)")
	}
	dbs, _ := a.store.ListDatabasesByAccount(ctx, acc.ID)
	allSites, _ := a.store.ListWebsites(ctx, acc.ID)
	width := 0
	for _, w := range la.Units {
		if len(w.UnitPath) > width {
			width = len(w.UnitPath)
		}
	}
	for j, w := range la.Units {
		dbCount := 0
		for _, d := range dbs {
			if d.WebsiteDomain == w.Domain {
				dbCount++
			}
		}
		boxes, _ := a.store.ListMailboxes(ctx, []string{w.Domain})
		line := fmt.Sprintf("    %d. %-*s  %s · %s", j+1, width, w.UnitPath, typeLabel(w), sizeLabel(w.SizeBytes))
		if detail {
			line += fmt.Sprintf(" · DB %d · Mailbox %d", dbCount, len(boxes))
			if w.PHPVersion != "" {
				line += " · PHP " + w.PHPVersion
			}
		}
		a.println(line + flags(w))
		if !detail {
			continue
		}
		var subs []string
		seenSub := map[string]bool{}
		for _, o := range allSites {
			if o.IncludedIn == w.Domain && !seenSub[o.Domain] {
				seenSub[o.Domain] = true
				subs = append(subs, o.Domain)
			}
		}
		for _, sd := range append(append([]string{}, w.Subdomains...), w.Parked...) {
			if !seenSub[sd] {
				seenSub[sd] = true
				subs = append(subs, sd)
			}
		}
		if len(subs) > 0 {
			a.printf("       subdomain/parked ikut folder ini: %s\n", strings.Join(subs, ", "))
		}
		for _, d := range dbs {
			if d.WebsiteDomain == w.Domain {
				src := d.Source
				if !d.InAPI {
					src += ", tidak ada di API"
				}
				size := sizeLabel(d.SizeBytes)
				if d.SizeBytes == 0 {
					size = "?"
					if d.InAPI {
						size = "< 1 MB"
					}
				}
				a.printf("       database: %s (%s, %s)\n", d.Name, size, src)
			}
		}
		for _, note := range w.Notes {
			a.printf("       catatan: %s\n", a.redactor.String(note))
		}
	}
	if !detail {
		return
	}
	var orphans []string
	for _, d := range dbs {
		if d.WebsiteDomain == "" {
			orphans = append(orphans, d.Name)
		}
	}
	if len(orphans) > 0 {
		a.printf("    Database tanpa website: %s\n", strings.Join(orphans, ", "))
	}
	if cron, _ := a.store.ListCron(ctx, acc.ID); len(cron) > 0 {
		a.printf("    Cron job: %d (dipindah saat migrasi)\n", len(cron))
		for _, c := range cron {
			a.printf("       %s  %s\n", c.Schedule, a.redactor.String(c.Command))
		}
	}
	if run, err := a.store.GetInventoryRun(ctx, acc.ID, acc.Alias); err == nil {
		a.printf("    Dipindai: %s · file JSON: %s\n", run.FinishedAt, run.JSONPath)
		for _, w := range run.Warnings {
			a.printf("    PERINGATAN: %s\n", a.redactor.String(w))
		}
	}
}
