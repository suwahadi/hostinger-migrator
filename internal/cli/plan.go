package cli

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/planner"
	"hostinger-migrator/internal/store"
)

type planOptions struct {
	dryRun        bool
	sourceAlias   string
	sourceAccount string
	websites      string
	destAlias     string
	destAccount   string
}

func newPlanCmd(app *App) *cobra.Command {
	var o planOptions
	cmd := &cobra.Command{
		Use:   "plan",
		Short: "Buat rencana migrasi (pilih sumber, website, tujuan) dan jalankan semua pemeriksaan",
		Long: "Membuat rencana migrasi satu akun sumber ke satu akun tujuan.\n" +
			"Pemeriksaan: token aktif, inventaris, bentrok domain, kuota website, disk (ukuran × 1,3), kuota database, SSH kedua sisi.\n" +
			"Dengan --dry-run hanya menampilkan pemeriksaan dan urutan langkah tanpa menyimpan atau mengubah apa pun.",
		Example: "  migrator plan\n  migrator plan --dry-run\n  migrator plan --sumber xyz --akun-sumber u494983133 --web domainanda.com,toko.id --tujuan zyx --akun-tujuan u73839202",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runPlan(cmd.Context(), o)
		},
	}
	cmd.Flags().BoolVar(&o.dryRun, "dry-run", false, "jalankan pemeriksaan dan tampilkan urutan langkah tanpa menyimpan")
	cmd.Flags().StringVar(&o.sourceAlias, "sumber", "", "alias token sumber")
	cmd.Flags().StringVar(&o.sourceAccount, "akun-sumber", "", "akun hosting sumber (uXXXXXXXX)")
	cmd.Flags().StringVar(&o.websites, "web", "", "daftar domain dipisah koma, atau 'semua'")
	cmd.Flags().StringVar(&o.destAlias, "tujuan", "", "alias token tujuan")
	cmd.Flags().StringVar(&o.destAccount, "akun-tujuan", "", "akun hosting tujuan (uXXXXXXXX)")
	return cmd
}

func (a *App) planner() *planner.Planner {
	return &planner.Planner{Store: a.store, Quotas: a.cfg.KuotaPaket, NewClient: a.tokenClient, Now: a.env.Now}
}

func (a *App) chooseAlias(ctx context.Context, role string) (string, error) {
	creds, err := a.store.ListCredentials(ctx)
	if err != nil {
		return "", err
	}
	for {
		a.printf("\nToken %s:\n", role)
		for i, c := range creds {
			a.printf("%d. %s (%s)\n", i+1, c.Alias, c.LastStatus)
		}
		a.println("B. Tambah token baru")
		v, err := a.prompt.Line("Pilih: ")
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if strings.EqualFold(v, "B") {
			alias, err := a.addTokenInteractive(ctx)
			if err != nil {
				a.printError(err.Error())
				creds, _ = a.store.ListCredentials(ctx)
				continue
			}
			return alias, nil
		}
		for i, c := range creds {
			if v == strconv.Itoa(i+1) || v == c.Alias {
				return c.Alias, nil
			}
		}
		a.say(toneWarn, "Pilihan tidak dikenal.")
	}
}

func (a *App) addTokenInteractive(ctx context.Context) (string, error) {
	before, _ := a.store.ListCredentials(ctx)
	if err := a.accountAdd(ctx, "", false); err != nil {
		return "", err
	}
	after, _ := a.store.ListCredentials(ctx)
	known := map[string]bool{}
	for _, c := range before {
		known[c.Alias] = true
	}
	for _, c := range after {
		if !known[c.Alias] {
			return c.Alias, nil
		}
	}
	return "", errors.New("token baru tidak ditemukan")
}

func (a *App) runPlan(ctx context.Context, o planOptions) error {
	var err error
	if o.sourceAlias == "" {
		if o.sourceAlias, err = a.chooseAlias(ctx, "sumber"); err != nil {
			return err
		}
	}
	if _, err := a.store.GetCredential(ctx, o.sourceAlias); err != nil {
		return needsAction(fmt.Errorf("alias sumber %s tidak terdaftar", o.sourceAlias))
	}
	if err := a.ensureInventory(ctx, o.sourceAlias, inventoryOptions{}); err != nil {
		return err
	}
	listed, err := a.listInventory(ctx, o.sourceAlias)
	if err != nil {
		return err
	}
	var domains []string
	if o.websites != "" {
		o.sourceAccount, domains, err = resolveWebsiteFlag(listed, o.sourceAccount, o.websites)
		if err != nil {
			return needsAction(err)
		}
	} else {
		total := 0
		for _, la := range listed {
			total += len(la.Units)
		}
		a.printf("\nToken %s: %d akun, %d website.\n", o.sourceAlias, len(listed), total)
		o.sourceAccount, domains, err = a.selectWebsites(listed, o.sourceAccount)
		if err != nil {
			return err
		}
	}

	if o.destAlias == "" {
		if o.destAlias, err = a.chooseAlias(ctx, "tujuan"); err != nil {
			return err
		}
	}
	if o.destAccount == "" {
		if o.destAccount, err = a.chooseDestAccount(ctx, o.destAlias, o.sourceAccount); err != nil {
			return err
		}
	}

	pl := a.planner()
	plan, err := pl.Build(ctx, planner.Request{SourceAlias: o.sourceAlias, SourceAccount: o.sourceAccount, Domains: domains, DestAlias: o.destAlias, DestAccount: o.destAccount})
	if err != nil {
		return needsAction(err)
	}
	a.printPlan(plan)
	if o.dryRun {
		a.printSteps(plan)
		a.println()
		if plan.Rejected() {
			a.say(toneDanger, "RENCANA DITOLAK (dry-run). Tidak ada yang disimpan atau diubah.")
			return needsAction(rejectError(plan))
		}
		a.say(toneOK, "DRY-RUN selesai. Tidak ada yang disimpan atau diubah. Jalankan tanpa --dry-run untuk menyimpan rencana.")
		return nil
	}
	if plan.Rejected() {
		a.println()
		a.say(toneDanger, "RENCANA DITOLAK. Perbaiki penyebab di atas lalu ulangi.")
		return needsAction(rejectError(plan))
	}
	if w := plan.Warnings(); len(w) > 0 {
		a.println()
		ok, err := a.prompt.Confirm(a.color(toneWarn, fmt.Sprintf("Ada %d peringatan di atas. Ketik LANJUT untuk menerimanya: ", len(w))), "LANJUT")
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan. Rencana tidak disimpan.")
			return nil
		}
	}
	a.println()
	ok, err := a.prompt.Confirm(a.color(toneOK, fmt.Sprintf("Konfirmasi: Anda yakin memindahkan domain dan layanan terkait\ndi akun %s %s ke tujuan %s %s? Ketik YAKIN: ", o.sourceAlias, o.sourceAccount, o.destAlias, o.destAccount)), "YAKIN")
	if err != nil {
		return err
	}
	if !ok {
		a.println("Dibatalkan. Rencana tidak disimpan.")
		return nil
	}
	id, err := pl.Save(ctx, plan)
	if err != nil {
		return err
	}
	a.println()
	a.say(toneOK, fmt.Sprintf("Batch %s tersimpan dengan status %s (%d website).", id, store.BatchPlanned, len(plan.ActiveItems())))
	return a.startWorker(ctx, false)
}

func rejectError(p *planner.Plan) error {
	var parts []string
	for _, c := range p.Failures() {
		parts = append(parts, c.Code)
	}
	return fmt.Errorf("%w: %s", planner.ErrRejected, strings.Join(parts, ", "))
}

func resolveWebsiteFlag(listed []listedAccount, account, web string) (string, []string, error) {
	var candidates []listedAccount
	for _, la := range listed {
		if account == "" || la.Account.ID == account {
			candidates = append(candidates, la)
		}
	}
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("akun sumber %s tidak ditemukan", account)
	}
	if strings.EqualFold(strings.TrimSpace(web), "semua") {
		if len(candidates) != 1 {
			return "", nil, errors.New("--web semua membutuhkan --akun-sumber karena token memiliki lebih dari satu akun")
		}
		var out []string
		for _, w := range candidates[0].Units {
			out = append(out, w.Domain)
		}
		return candidates[0].Account.ID, out, nil
	}
	var domains []string
	for _, d := range strings.Split(web, ",") {
		if d = strings.TrimSpace(d); d != "" {
			domains = append(domains, d)
		}
	}
	if account != "" {
		return account, domains, nil
	}
	owner := ""
	for _, d := range domains {
		for _, la := range candidates {
			for _, w := range la.Units {
				if strings.EqualFold(w.Domain, d) {
					if owner != "" && owner != la.Account.ID {
						return "", nil, errors.New("website yang dipilih berada di lebih dari satu akun; satu batch hanya untuk satu akun sumber")
					}
					owner = la.Account.ID
				}
			}
		}
	}
	if owner == "" {
		if len(candidates) == 1 {
			return candidates[0].Account.ID, domains, nil
		}
		return "", nil, errors.New("akun sumber tidak bisa ditentukan; gunakan --akun-sumber")
	}
	return owner, domains, nil
}

func (a *App) selectWebsites(listed []listedAccount, presetAccount string) (string, []string, error) {
	selected := map[string]bool{}
	var order []string
	account := presetAccount
	add := func(hits []websiteHit) {
		mixed := false
		for _, h := range hits {
			if account == "" {
				account = h.Account
			}
			if h.Account != account {
				mixed = true
				continue
			}
			if !selected[h.Site.Domain] {
				selected[h.Site.Domain] = true
				order = append(order, h.Site.Domain)
			}
		}
		if mixed {
			a.say(toneWarn, fmt.Sprintf("Satu batch hanya untuk satu akun sumber (%s). Pilihan dari akun lain diabaikan.", account))
		}
		if len(order) > 0 {
			a.say(toneOK, fmt.Sprintf("Terpilih (%s): %s", account, strings.Join(order, ", ")))
		}
	}
	a.println("Ketik sebagian domain atau id akun. ENTER = selesai.")
	for {
		v, err := a.prompt.Line("\nCari: ")
		if err != nil {
			return "", nil, err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			if len(order) == 0 {
				a.say(toneWarn, "Belum ada website dipilih.")
				continue
			}
			return account, order, nil
		}
		if hits, missing, exact := exactWebsites(listed, v); exact {
			for _, m := range missing {
				a.say(toneWarn, fmt.Sprintf("Domain %q tidak ditemukan.", m))
			}
			add(hits)
			continue
		}
		hits := matchWebsites(listed, v)
		if len(hits) == 0 {
			a.say(toneWarn, fmt.Sprintf("Tidak ada website yang cocok dengan %q.", v))
			continue
		}
		shown := hits[:min(len(hits), maxResults)]
		width := 0
		for _, h := range shown {
			width = max(width, len(h.Site.Domain))
		}
		a.printf("Hasil %q (%d):\n", v, len(hits))
		for i, h := range shown {
			a.printf("%2d. %s  %-*s  %s · %s%s\n", i+1, h.Account, width, h.Site.Domain, typeLabel(h.Site), sizeLabel(h.Site.SizeBytes), flags(h.Site))
		}
		if len(hits) > len(shown) {
			a.say(toneWarn, fmt.Sprintf("Menampilkan %d dari %d. Persempit kata kunci.", len(shown), len(hits)))
		}
		picks, err := a.pickHits(shown)
		if err != nil {
			return "", nil, err
		}
		add(picks)
	}
}

func (a *App) pickHits(shown []websiteHit) ([]websiteHit, error) {
	for {
		v, err := a.prompt.Line("Pilih nomor (pisah koma), A = semua hasil, ENTER = cari lagi: ")
		if err != nil {
			return nil, err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			return nil, nil
		}
		if strings.EqualFold(v, "A") {
			return shown, nil
		}
		var picks []websiteHit
		bad := ""
		for _, part := range strings.Split(v, ",") {
			part = strings.TrimSpace(part)
			n, err := strconv.Atoi(part)
			if err != nil || n < 1 || n > len(shown) {
				bad = part
				break
			}
			picks = append(picks, shown[n-1])
		}
		if bad == "" {
			return picks, nil
		}
		a.say(toneWarn, fmt.Sprintf("Pilihan %q tidak valid.", bad))
	}
}

func (a *App) chooseDestAccount(ctx context.Context, alias, sourceAccount string) (string, error) {
	accs, err := a.store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return "", err
	}
	var options []store.HostingAccount
	for _, acc := range accs {
		if acc.ID != sourceAccount {
			options = append(options, acc)
		}
	}
	if len(options) == 0 {
		return "", needsAction(fmt.Errorf("token %s tidak memiliki akun hosting tujuan selain akun sumber", alias))
	}
	pl := a.planner()
	a.printf("Ditemukan %d akun di token %s:\n", len(options), alias)
	for i, acc := range options {
		label := "sisa kuota website: ?"
		if q, _, err := pl.DestQuota(ctx, alias, acc); err == nil {
			if q.Known {
				label = fmt.Sprintf("sisa kuota website: %d", q.Remaining)
			} else {
				label = fmt.Sprintf("kuota website tidak diketahui, terpakai %d", q.Used)
			}
		} else {
			label = "kuota tidak terbaca: " + a.redactor.String(err.Error())
		}
		a.printf("%d. %s  (%s) — %s\n", i+1, acc.ID, PlanLabel(acc.Plan), label)
	}
	for {
		v, err := a.prompt.Line("Pilih salah satu: ")
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		for i, acc := range options {
			if v == strconv.Itoa(i+1) || v == acc.ID {
				return acc.ID, nil
			}
		}
		a.say(toneWarn, "Pilihan tidak dikenal.")
	}
}

func checkTag(status string) string {
	switch status {
	case planner.CheckOK:
		return "[OK]        "
	case planner.CheckWarn:
		return "[PERINGATAN]"
	default:
		return "[GAGAL]     "
	}
}

func (a *App) printPlan(p *planner.Plan) {
	r := p.Request
	a.println()
	a.say(toneTitle, "RINGKASAN RENCANA")
	a.printf("Sumber : %s / %s (%d website)\n", r.SourceAlias, r.SourceAccount, len(p.ActiveItems()))
	a.printf("Tujuan : %s / %s (%s)\n", r.DestAlias, r.DestAccount, PlanLabel(p.DestPlan))
	size := planner.HumanBytes(p.TotalSize)
	if p.UnknownSize > 0 {
		size += fmt.Sprintf(" + %d website ukuran tidak diketahui", p.UnknownSize)
	}
	a.printf("Perkiraan backup: %s · Database: %d · Mailbox: %d\n", size, p.DBCount, p.MailboxCount)
	if len(p.Items) > 0 {
		a.println("Website:")
		for _, it := range p.Items {
			status := "→ " + it.ExpectedFinal
			if it.Blocked {
				status = "→ BLOCKED: " + it.BlockReason
			}
			line := fmt.Sprintf("  %s  %s · %s %s", it.Website.Domain, typeLabel(it.Website), sizeLabel(it.Website.SizeBytes), status)
			if it.Blocked {
				line = a.color(toneDanger, line)
			}
			a.println(line)
			for _, n := range it.Notes {
				a.printf("      catatan: %s\n", n)
			}
		}
	}
	a.println("Pemeriksaan:")
	for _, c := range p.Checks {
		a.say(checkTone(c.Status), fmt.Sprintf("  %s %-16s %s", checkTag(c.Status), c.Code, c.Message))
	}
}

func checkTone(status string) tone {
	switch status {
	case planner.CheckOK:
		return toneOK
	case planner.CheckWarn:
		return toneWarn
	}
	return toneDanger
}

func (a *App) printSteps(p *planner.Plan) {
	a.println()
	a.say(toneTitle, "URUTAN LANGKAH (dry-run, tidak ada operasi tulis)")
	for _, it := range p.Items {
		a.printf("%s:\n", it.Website.Domain)
		for i, s := range it.Steps {
			a.printf("  %2d. %s\n", i+1, s)
		}
	}
}
