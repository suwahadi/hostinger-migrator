package planner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"hostinger-migrator/internal/capability"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/store"
)

const (
	CheckOK   = "OK"
	CheckWarn = "PERINGATAN"
	CheckFail = "GAGAL"

	FinalCompleted = "COMPLETED"
	FinalDummyOnly = "DUMMY_ONLY"
)

var ErrRejected = errors.New("rencana ditolak")

type Request struct {
	SourceAlias   string
	SourceAccount string
	Domains       []string
	DestAlias     string
	DestAccount   string
}

type Check struct {
	Code    string
	Status  string
	Message string
}

type Item struct {
	Website       store.Website
	Databases     []store.DatabaseRow
	Mailboxes     []store.MailboxRow
	Included      []string
	ExpectedFinal string
	Blocked       bool
	BlockReason   string
	ManualBackup  bool
	Notes         []string
	Steps         []string
}

type Plan struct {
	Request      Request
	SourcePlan   string
	DestPlan     string
	Items        []Item
	Checks       []Check
	TotalSize    int64
	UnknownSize  int
	DBCount      int
	MailboxCount int
	CreatedAt    time.Time
}

func (p *Plan) Rejected() bool {
	for _, c := range p.Checks {
		if c.Status == CheckFail {
			return true
		}
	}
	return false
}

func (p *Plan) Warnings() []Check {
	var out []Check
	for _, c := range p.Checks {
		if c.Status == CheckWarn {
			out = append(out, c)
		}
	}
	return out
}

func (p *Plan) Failures() []Check {
	var out []Check
	for _, c := range p.Checks {
		if c.Status == CheckFail {
			out = append(out, c)
		}
	}
	return out
}

func (p *Plan) ActiveItems() []Item {
	var out []Item
	for _, it := range p.Items {
		if !it.Blocked {
			out = append(out, it)
		}
	}
	return out
}

type Planner struct {
	Store     *store.Store
	NewClient func(alias string) (*hostinger.Client, error)
	Now       func() time.Time
	MaxInvAge time.Duration
}

func (pl *Planner) now() time.Time {
	if pl.Now != nil {
		return pl.Now()
	}
	return time.Now()
}

func (pl *Planner) destSites(ctx context.Context, alias string, acc store.HostingAccount) ([]hostinger.Website, error) {
	client, err := pl.NewClient(alias)
	if err != nil {
		return nil, err
	}
	return client.ReadOnlyCopy().ListWebsites(ctx, hostinger.WebsiteFilter{Username: acc.ID})
}

func (pl *Planner) Build(ctx context.Context, req Request) (*Plan, error) {
	p := &Plan{Request: req, CreatedAt: pl.now()}
	add := func(code, status, msg string) {
		p.Checks = append(p.Checks, Check{Code: code, Status: status, Message: msg})
	}

	srcAcc, err := pl.account(ctx, req.SourceAlias, req.SourceAccount)
	if err != nil {
		return nil, err
	}
	dstAcc, err := pl.account(ctx, req.DestAlias, req.DestAccount)
	if err != nil {
		return nil, err
	}
	p.SourcePlan, p.DestPlan = srcAcc.Plan, dstAcc.Plan

	for _, side := range []struct{ code, alias string }{{"TOKEN_SUMBER", req.SourceAlias}, {"TOKEN_TUJUAN", req.DestAlias}} {
		cred, err := pl.Store.GetCredential(ctx, side.alias)
		switch {
		case err != nil:
			add(side.code, CheckFail, fmt.Sprintf("Token %s tidak terdaftar.", side.alias))
		case cred.LastStatus != store.CredentialActive:
			add(side.code, CheckFail, fmt.Sprintf("Token %s berstatus %s; jalankan 'migrator account test %s'.", side.alias, cred.LastStatus, side.alias))
		default:
			add(side.code, CheckOK, fmt.Sprintf("Token %s aktif.", side.alias))
		}
	}
	if req.SourceAccount == req.DestAccount {
		add("AKUN_BERBEDA", CheckFail, "Akun sumber dan tujuan sama.")
	}

	run, err := pl.Store.GetInventoryRun(ctx, req.SourceAccount, req.SourceAlias)
	if err != nil {
		add("INVENTARIS", CheckFail, fmt.Sprintf("Inventaris akun %s belum ada; jalankan 'migrator inventory %s'.", req.SourceAccount, req.SourceAlias))
		return p, nil
	}
	if t, perr := time.Parse(time.RFC3339, run.FinishedAt); perr == nil {
		age := pl.now().Sub(t)
		maxAge := pl.MaxInvAge
		if maxAge <= 0 {
			maxAge = 24 * time.Hour
		}
		if age > maxAge {
			add("INVENTARIS", CheckWarn, fmt.Sprintf("Inventaris berumur %s; sebaiknya jalankan 'migrator inventory %s --refresh'.", age.Round(time.Hour), req.SourceAlias))
		} else {
			add("INVENTARIS", CheckOK, "Inventaris dipindai "+run.FinishedAt+".")
		}
	}

	websites, err := pl.Store.ListWebsites(ctx, req.SourceAccount)
	if err != nil {
		return nil, err
	}
	byDomain := map[string]store.Website{}
	for _, w := range websites {
		byDomain[strings.ToLower(w.Domain)] = w
	}
	dbs, err := pl.Store.ListDatabasesByAccount(ctx, req.SourceAccount)
	if err != nil {
		return nil, err
	}
	active, err := pl.Store.ActiveItemDomains(ctx, req.SourceAccount)
	if err != nil {
		return nil, err
	}

	var invalid []string
	seen := map[string]bool{}
	for _, d := range req.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		w, ok := byDomain[d]
		if !ok {
			invalid = append(invalid, d+" (tidak ada di inventaris)")
			continue
		}
		if !w.IsUnit() {
			reason := "bukan unit backup"
			if w.IncludedIn != "" {
				reason = "ikut folder " + w.IncludedIn
			}
			invalid = append(invalid, d+" ("+reason+")")
			continue
		}
		item := Item{Website: w, ExpectedFinal: FinalCompleted}
		if w.DummyOnly() {
			item.ExpectedFinal = FinalDummyOnly
			item.Notes = append(item.Notes, fmt.Sprintf("Domain %s; dipindah ke domain dummy dan berakhir DUMMY_ONLY.", strings.ToLower(w.DomainStatus)))
		}
		if b, ok := active[w.Domain]; ok {
			item.Blocked = true
			item.BlockReason = "Sudah ada di batch " + b + " yang belum selesai."
		}
		for _, db := range dbs {
			if db.WebsiteDomain == w.Domain {
				item.Databases = append(item.Databases, db)
			}
		}
		for _, other := range websites {
			if other.IncludedIn == w.Domain {
				item.Included = append(item.Included, other.Domain)
			}
		}
		if w.NSExternal != nil && *w.NSExternal {
			item.Notes = append(item.Notes, "Nameserver eksternal ("+strings.Join(w.Nameservers, ", ")+"); perubahan DNS saat cutover menjadi checkpoint manual.")
		}
		if w.SiteType == "PHP native" && len(item.Databases) == 0 {
			item.Notes = append(item.Notes, "PHP native tanpa database terdeteksi; konfirmasi apakah website memakai database.")
		}
		if (w.SiteType == "WordPress" || w.SiteType == "Laravel" || w.SiteType == "Joomla" || w.SiteType == "CodeIgniter") && len(item.Databases) == 0 {
			item.Notes = append(item.Notes, "Tipe "+w.SiteType+" tetapi database tidak terdeteksi; periksa file konfigurasi.")
		}
		p.Items = append(p.Items, item)
	}
	if len(invalid) > 0 {
		add("PILIHAN_WEBSITE", CheckFail, "Website tidak bisa dipilih: "+strings.Join(invalid, ", ")+".")
	}
	if len(p.Items) == 0 {
		add("PILIHAN_WEBSITE", CheckFail, "Tidak ada website yang dipilih.")
		return p, nil
	}
	if len(invalid) == 0 {
		add("PILIHAN_WEBSITE", CheckOK, fmt.Sprintf("%d website dipilih.", len(p.Items)))
	}

	destSites, err := pl.destSites(ctx, req.DestAlias, dstAcc)
	if err != nil {
		add("BENTROK_DOMAIN", CheckFail, "Tidak bisa membaca website akun tujuan: "+err.Error())
	} else {
		destDomains := map[string]bool{}
		for _, s := range destSites {
			destDomains[strings.ToLower(s.Domain)] = true
		}
		var clash []string
		for i := range p.Items {
			it := &p.Items[i]
			names := append([]string{it.Website.Domain}, it.Included...)
			names = append(names, it.Website.Parked...)
			for _, n := range names {
				if destDomains[strings.ToLower(n)] {
					it.Blocked = true
					it.BlockReason = "Domain " + n + " sudah ada di akun tujuan; tidak akan ditimpa."
					clash = append(clash, n)
				}
			}
		}
		switch {
		case len(clash) == 0:
			add("BENTROK_DOMAIN", CheckOK, "Tidak ada domain yang bentrok di akun tujuan.")
		case len(p.ActiveItems()) == 0:
			add("BENTROK_DOMAIN", CheckFail, "Semua website bentrok dengan akun tujuan: "+strings.Join(clash, ", ")+".")
		default:
			add("BENTROK_DOMAIN", CheckWarn, "Domain sudah ada di akun tujuan dan item ditandai BLOCKED: "+strings.Join(clash, ", ")+".")
		}
	}

	activeItems := p.ActiveItems()
	for _, it := range p.Items {
		if it.Blocked && !strings.Contains(it.BlockReason, "akun tujuan") {
			add("BATCH_LAIN", CheckWarn, it.Website.Domain+": "+it.BlockReason)
		}
	}
	for _, it := range activeItems {
		if it.Website.SizeBytes >= 0 {
			p.TotalSize += it.Website.SizeBytes
		} else {
			p.UnknownSize++
		}
		for _, db := range it.Databases {
			p.DBCount++
			if db.SizeBytes > 0 {
				p.TotalSize += db.SizeBytes
			}
		}
	}

	srcSSH, _ := pl.Store.GetSSH(ctx, req.SourceAccount)
	if srcSSH != nil && srcSSH.Status == store.SSHReady {
		add("SSH_SUMBER", CheckOK, fmt.Sprintf("SSH sumber %s siap.", req.SourceAccount))
		var unknown []string
		for _, it := range p.Items {
			if it.Blocked {
				continue
			}
			for _, d := range it.Databases {
				if d.Source == "" || d.Source == "api" {
					unknown = append(unknown, fmt.Sprintf("%s (%s)", it.Website.Domain, d.Name))
				}
			}
		}
		if len(unknown) > 0 {
			add("KREDENSIAL_DB", CheckWarn, fmt.Sprintf("Sumber password database belum diketahui: %s. Jalankan 'migrator inventory --akun %s --refresh' sebelum migrasi; tanpa itu backup berhenti di tindakan manual MANUAL_DB_SOURCE.", strings.Join(unknown, ", "), req.SourceAccount))
		}
	} else {
		status := store.SSHNotConfigured
		if srcSSH != nil {
			status = srcSSH.Status
		}
		add("SSH_SUMBER", CheckWarn, fmt.Sprintf("SSH sumber berstatus %s; semua backup memakai checkpoint MANUAL_BACKUP (unduh dari hPanel).", status))
		for i := range p.Items {
			p.Items[i].ManualBackup = true
		}
	}
	dstSSH, _ := pl.Store.GetSSH(ctx, req.DestAccount)
	if dstSSH != nil && dstSSH.Status == store.SSHReady {
		add("SSH_TUJUAN", CheckOK, fmt.Sprintf("SSH tujuan %s siap.", req.DestAccount))
	} else {
		status := store.SSHNotConfigured
		if dstSSH != nil {
			status = dstSSH.Status
		}
		add("SSH_TUJUAN", CheckFail, fmt.Sprintf("SSH tujuan berstatus %s; restore membutuhkan SSH. Jalankan 'migrator ssh setup %s'.", status, req.DestAccount))
	}

	domains := make([]string, 0, len(activeItems))
	for _, it := range activeItems {
		domains = append(domains, it.Website.Domain)
	}
	boxes, err := pl.Store.ListMailboxes(ctx, domains)
	if err != nil {
		return nil, err
	}
	for i := range p.Items {
		for _, b := range boxes {
			if b.Domain == p.Items[i].Website.Domain {
				p.Items[i].Mailboxes = append(p.Items[i].Mailboxes, b)
			}
		}
		if !p.Items[i].Blocked {
			p.MailboxCount += len(p.Items[i].Mailboxes)
		}
	}

	dummy := 0
	for _, it := range activeItems {
		if it.ExpectedFinal == FinalDummyOnly {
			dummy++
		}
	}
	if dummy > 0 {
		add("DUMMY_ONLY", CheckOK, fmt.Sprintf("%d website dengan domain expired/disabled tetap dipindah dan berakhir DUMMY_ONLY.", dummy))
	}

	caps := pl.capabilities(ctx, req.DestAccount)
	srcCaps := pl.capabilities(ctx, req.SourceAccount)
	for i := range p.Items {
		p.Items[i].Steps = steps(p.Items[i], caps, srcCaps)
	}
	return p, nil
}

func (pl *Planner) account(ctx context.Context, alias, id string) (store.HostingAccount, error) {
	accs, err := pl.Store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return store.HostingAccount{}, err
	}
	for _, a := range accs {
		if a.ID == id {
			return a, nil
		}
	}
	return store.HostingAccount{}, fmt.Errorf("akun hosting %s tidak ditemukan di token %s", id, alias)
}

func (pl *Planner) capabilities(ctx context.Context, accountID string) map[string]store.Capability {
	out := map[string]store.Capability{}
	caps, _ := pl.Store.ListCapabilities(ctx, accountID)
	for _, c := range caps {
		out[c.Code] = c
	}
	return out
}

func capNote(caps map[string]store.Capability, code, manual string) string {
	if c, ok := caps[code]; ok && c.Status == "SUPPORTED" {
		return "via API"
	}
	return "via API bila berhasil, jika tidak checkpoint " + manual
}

func steps(it Item, dest, src map[string]store.Capability) []string {
	if it.Blocked {
		return []string{"Tidak diproses: " + it.BlockReason}
	}
	w := it.Website
	var out []string
	if it.ManualBackup {
		out = append(out, "Backup file + database: checkpoint MANUAL_BACKUP (unduh dari hPanel), lalu divalidasi tool")
	} else {
		out = append(out, "Backup file: tar seluruh folder "+w.UnitPath+" via SSH → files.tar.gz + SHA-256")
		for _, db := range it.Databases {
			out = append(out, "Backup database "+db.Name+": mysqldump via SSH → db-"+db.Name+".sql.gz + jumlah baris per tabel")
		}
	}
	out = append(out, "Buat website dummy (subdomain gratis) di akun tujuan "+capNote(dest, "API_CREATE_WEBSITE", "MANUAL_CREATE_SITE"))
	out = append(out, "Upload + ekstrak arsip ke folder dummy via SFTP/SSH, isi bawaan dipindah ke .migrator/")
	for _, db := range it.Databases {
		out = append(out, "Buat database tujuan untuk "+db.Name+" "+capNote(dest, "API_DB_CREATE", "MANUAL_CREATE_DB")+", import, cocokkan jumlah tabel & baris")
	}
	if len(it.Databases) > 0 {
		out = append(out, "Perbarui kredensial DB di file konfigurasi (salinan asli .migrator-orig)")
	}
	if w.PHPVersion != "" {
		out = append(out, "Samakan PHP "+w.PHPVersion+" "+capNote(dest, "API_PHP", "manual"))
	}
	switch w.SiteType {
	case "Statis", "Node.js":
		out = append(out, "Verifikasi: GET langsung ke URL dummy")
	default:
		out = append(out, "Verifikasi: request ke IP server tujuan dengan header Host: "+w.Domain)
	}
	if it.ExpectedFinal == FinalDummyOnly {
		out = append(out, "Selesai sebagai DUMMY_ONLY (tanpa cutover)")
		return out
	}
	out = append(out, "Backup final + snapshot DNS zone "+w.Domain)
	out = append(out, "Lepas domain di sumber: checkpoint MANUAL_SOURCE_RENAME")
	out = append(out, "Pasang domain di tujuan (parked domain/onboarding, diuji Fase 4) atau MANUAL_DEST_DOMAIN")
	if w.NSExternal != nil && *w.NSExternal {
		out = append(out, "Ubah DNS di nameserver eksternal: checkpoint manual")
	} else {
		out = append(out, "Ubah A/AAAA ke IP tujuan dengan token sumber; record pihak ketiga dipertahankan")
	}
	out = append(out, "Pasang SSL "+capNote(dest, "API_SSL", "manual")+", verifikasi HTTP & HTTPS")
	if len(it.Mailboxes) > 0 {
		out = append(out, fmt.Sprintf("Email: %d mailbox (backup IMAP → hapus lama → buat baru → IMAP APPEND → DNS email)", len(it.Mailboxes)))
	}
	out = append(out, "Cleanup sumber hanya setelah semua gerbang lolos dan konfirmasi HAPUS")
	return out
}

type planJSON struct {
	Source    string   `json:"sumber"`
	Dest      string   `json:"tujuan"`
	Domains   []string `json:"website"`
	TotalSize int64    `json:"perkiraan_ukuran_byte"`
	DBCount   int      `json:"jumlah_database"`
	Mailboxes int      `json:"jumlah_mailbox"`
	Checks    []Check  `json:"pemeriksaan"`
}

func (pl *Planner) Save(ctx context.Context, p *Plan) (string, error) {
	if p.Rejected() {
		return "", ErrRejected
	}
	var items []store.Item
	var domains []string
	for _, it := range p.Items {
		state := store.ItemPlanned
		lastErr := ""
		if it.Blocked {
			state = store.ItemBlocked
			lastErr = it.BlockReason
		} else {
			domains = append(domains, it.Website.Domain)
		}
		items = append(items, store.Item{WebsiteID: it.Website.ID, SourceDomain: it.Website.Domain, State: state, ExpectedFinal: it.ExpectedFinal, PlanNotes: strings.Join(it.Notes, " "), LastError: lastErr})
	}
	sort.Strings(domains)
	doc, _ := json.Marshal(planJSON{
		Source: p.Request.SourceAlias + "/" + p.Request.SourceAccount, Dest: p.Request.DestAlias + "/" + p.Request.DestAccount,
		Domains: domains, TotalSize: p.TotalSize, DBCount: p.DBCount, Mailboxes: p.MailboxCount, Checks: p.Checks,
	})
	return pl.Store.CreateBatch(ctx, store.Batch{
		SourceAlias: p.Request.SourceAlias, SourceAccount: p.Request.SourceAccount,
		DestAlias: p.Request.DestAlias, DestAccount: p.Request.DestAccount, PlanJSON: string(doc),
	}, items)
}

var HumanBytes = capability.HumanBytes
