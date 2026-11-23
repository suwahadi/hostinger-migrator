package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/capability"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/inventory"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

func newInventoryCmd(app *App) *cobra.Command {
	var opt inventoryOptions
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
			return app.runInventory(cmd.Context(), alias, opt)
		},
	}
	cmd.Flags().BoolVar(&opt.Refresh, "refresh", false, "pindai ulang walaupun inventaris sudah ada")
	cmd.Flags().BoolVar(&opt.Quick, "cepat", false, "scan cepat: ukuran folder dan jumlah file tidak dihitung")
	cmd.Flags().StringVar(&opt.Account, "akun", "", "pindai ulang satu akun hosting saja (mis. u123456789)")
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

type inventoryOptions struct {
	Refresh bool
	Quick   bool
	Account string
}

func (a *App) inventoryProgress(p inventory.Progress) {
	switch p.Stage {
	case inventory.StageAccount:
		a.printf("Akun %s: %d website ditemukan, dipindai %d sekaligus.\n", p.AccountID, p.Total, min(inventory.DefaultParallel, max(p.Total, 1)))
	case inventory.StageWebsite:
		t := p.Type
		if t == "" {
			t = "?"
		}
		line := fmt.Sprintf("  [%d/%d] %s  %s  %s  %s", p.Done, p.Total, p.Domain, t, sizeLabel(p.SizeBytes), p.Elapsed.Round(time.Second))
		if p.Warnings > 0 {
			a.say(toneWarn, fmt.Sprintf("%s  (%d peringatan)", line, p.Warnings))
			return
		}
		a.println(line)
	}
}

func (a *App) ensureInventory(ctx context.Context, alias string, opt inventoryOptions) error {
	accs, err := a.store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return err
	}
	var targets, done []string
	for _, acc := range accs {
		switch {
		case opt.Account != "":
			if acc.ID == opt.Account {
				targets = append(targets, acc.ID)
			}
		case opt.Refresh:
			targets = append(targets, acc.ID)
		default:
			if _, err := a.store.GetInventoryRun(ctx, acc.ID, alias); errors.Is(err, store.ErrNotFound) {
				targets = append(targets, acc.ID)
			} else {
				done = append(done, acc.ID)
			}
		}
	}
	if len(targets) == 0 && (len(accs) > 0 || !opt.Refresh) {
		return nil
	}
	mode := ""
	if opt.Quick {
		mode = ", scan cepat tanpa hitung ukuran"
	}
	switch {
	case len(accs) == 0:
		a.printf("Memindai inventaris token %s (read-only%s)...\n", alias, mode)
	case len(done) > 0:
		a.printf("Melanjutkan inventaris token %s (read-only%s): %d akun belum dipindai, %d akun sudah ada dan dilewati (%s).\n", alias, mode, len(targets), len(done), strings.Join(done, ", "))
	case len(targets) < len(accs):
		a.printf("Memindai ulang akun %s dari token %s (read-only%s)...\n", strings.Join(targets, ", "), alias, mode)
	default:
		a.printf("Memindai inventaris token %s (read-only%s)...\n", alias, mode)
	}
	started := time.Now()
	sc := a.scanner()
	sc.SkipSize = opt.Quick
	sc.Progress = a.inventoryProgress
	if len(targets) == len(accs) {
		targets = nil
	}
	_, err = sc.ScanAccounts(ctx, alias, targets)
	if err == nil {
		a.say(toneOK, fmt.Sprintf("Pemindaian token %s selesai dalam %s.", alias, time.Since(started).Round(time.Second)))
	}
	if errors.Is(err, inventory.ErrTokenRejected) {
		a.store.WithTxUpdateCredential(ctx, alias, store.CredentialRejected, a.redactor.String(err.Error()))
		return needsAction(err)
	}
	if errors.Is(err, account.ErrTokenMissing) {
		return needsAction(err)
	}
	return err
}

func (a *App) runInventory(ctx context.Context, alias string, opt inventoryOptions) error {
	aliases, err := a.aliases(ctx, alias)
	if err != nil {
		return err
	}
	if opt.Account != "" {
		owner, err := a.accountAlias(ctx, alias, opt.Account)
		if err != nil {
			return err
		}
		aliases = []string{owner}
	}
	var failed []error
	for _, al := range aliases {
		if err := a.ensureInventory(ctx, al, opt); err != nil {
			var ee *exitError
			if errors.As(err, &ee) {
				a.printError(err.Error())
				failed = append(failed, err)
				continue
			}
			return err
		}
		a.println()
		a.say(toneTitle, "INVENTARIS — token "+al)
		if err := a.printAliasInventory(ctx, al, true); err != nil {
			return err
		}
	}
	a.println()
	a.println("Ukuran \"?\" berarti tidak diketahui (SSH belum siap atau scan cepat). Tipe diberi persen bila keyakinan deteksi di bawah 70%.")
	if len(failed) > 0 {
		return needsAction(errors.Join(failed...))
	}
	return nil
}

func (a *App) accountAlias(ctx context.Context, alias, accountID string) (string, error) {
	accs, err := a.store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return "", err
	}
	for _, acc := range accs {
		if acc.ID == accountID {
			return acc.Alias, nil
		}
	}
	if alias != "" {
		return "", needsAction(fmt.Errorf("akun hosting %s tidak terdaftar di token %s; lihat 'migrator account list'", accountID, alias))
	}
	return "", needsAction(fmt.Errorf("akun hosting %s tidak terdaftar; lihat 'migrator account list'", accountID))
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
			a.say(toneWarn, "    PERINGATAN: "+a.redactor.String(w))
		}
	}
}
