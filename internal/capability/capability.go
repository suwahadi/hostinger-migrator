package capability

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strconv"
	"strings"
	"time"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

const (
	Supported   = "SUPPORTED"
	Manual      = "MANUAL"
	Unsupported = "UNSUPPORTED"
	Unknown     = "UNKNOWN"
)

var ErrTokenRejected = errors.New("token ditolak Hostinger saat audit")

type Result struct {
	Code   string
	Status string
	Note   string
	Detail map[string]any
}

type Report struct {
	AccountID string
	Alias     string
	Plan      string
	Results   []Result
}

func (r Report) Count(status string) int {
	n := 0
	for _, x := range r.Results {
		if x.Status == status {
			n++
		}
	}
	return n
}

type Auditor struct {
	Store       *store.Store
	Registry    *hostinger.Registry
	NewClient   func(alias string) (*hostinger.Client, error)
	SSH         *sshx.Service
	Log         *slog.Logger
	CommandWait time.Duration
}

type contractCheck struct {
	code     string
	endpoint string
	phase    int
	note     string
}

var contractChecks = []contractCheck{
	{"API_CREATE_WEBSITE", "hosting.onboardings.start", 4, "Buat website via onboarding + polling."},
	{"API_FREE_SUBDOMAIN", "hosting.free_subdomains.generate", 4, "Subdomain dummy gratis."},
	{"API_DB_CREATE", "hosting.databases.create", 4, "Buat database tujuan."},
	{"API_PHP", "hosting.php.version.update", 4, "Samakan versi PHP."},
	{"API_DNS_WRITE", "dns.zones.update", 5, "Tulis zone hanya saat cutover."},
	{"API_SSL", "hosting.ssl.setup", 5, "Pasang SSL setelah DNS mengarah."},
	{"API_EMAIL_CREATE", "mail.mailboxes.create", 6, "Ketersediaan untuk Free Business Email belum terbukti (R9)."},
	{"API_EMAIL_DELETE", "mail.mailboxes.delete", 6, "Ketersediaan untuk Free Business Email belum terbukti (R9)."},
	{"API_EMAIL_PASSWORD", "mail.mailboxes.password.change", 6, "Dipakai untuk mengganti password mailbox sumber sebelum backup IMAP (D-010)."},
	{"API_EMAIL_BACKUP", "mailapi.messages.source", 6, "Butuh token Mail API (Agentic Mail); untuk Free Business Email dipakai IMAP FETCH (D-010)."},
}

type fixedCheck struct {
	code   string
	status string
	note   string
}

var fixedChecks = []fixedCheck{
	{"API_CHANGE_DOMAIN", Manual, "Tidak ada endpoint ganti domain untuk Shared Hosting. Checkpoint MANUAL_SOURCE_RENAME di sumber; domain tujuan dicoba via parked domain/onboarding di Fase 4 (D-011)."},
	{"API_SSH_KEY", Manual, "Tidak ada endpoint menambah SSH key login untuk Shared Hosting. Public key ditempel manual di hPanel (migrator ssh setup)."},
	{"API_EMAIL_RESTORE", Unsupported, "Tidak ada endpoint upload/append pesan. Restore memakai IMAP APPEND (D-008)."},
}

func (a *Auditor) AuditAlias(ctx context.Context, alias string) ([]Report, error) {
	accounts, err := a.Store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return nil, err
	}
	if len(accounts) == 0 {
		return nil, fmt.Errorf("token %s belum memiliki akun hosting tercatat; jalankan 'migrator account test %s'", alias, alias)
	}
	client, err := a.NewClient(alias)
	if err != nil {
		return nil, err
	}
	var reports []Report
	for _, acc := range accounts {
		rep, err := a.AuditAccount(ctx, client, alias, acc)
		if err != nil {
			return reports, err
		}
		reports = append(reports, rep)
	}
	return reports, nil
}

func (a *Auditor) AuditAccount(ctx context.Context, client *hostinger.Client, alias string, acc store.HostingAccount) (Report, error) {
	rep := Report{AccountID: acc.ID, Alias: alias, Plan: acc.Plan}
	add := func(r Result) { rep.Results = append(rep.Results, r) }

	sites, err := client.ListWebsites(ctx, hostinger.WebsiteFilter{Username: acc.ID})
	if err != nil {
		if hostinger.IsBlocking(err) {
			return rep, fmt.Errorf("%w: %v", ErrTokenRejected, err)
		}
		add(Result{Code: "API_LIST_WEBSITES", Status: Unknown, Note: "Gagal dibaca: " + err.Error()})
	} else {
		add(Result{Code: "API_LIST_WEBSITES", Status: Supported, Note: fmt.Sprintf("Teruji: %d website terbaca.", len(sites)), Detail: map[string]any{"jumlah_website": len(sites)}})
	}

	for _, c := range contractChecks {
		add(a.contractOnly(c))
	}
	for _, f := range fixedChecks {
		add(Result{Code: f.code, Status: f.status, Note: f.note})
	}

	add(a.liveDatabases(ctx, client, acc.ID))
	domain := pickDomain(sites)
	add(a.liveDNS(ctx, client, "API_DNS_READ", "dns.zones.get", domain))
	add(a.liveDNS(ctx, client, "API_DNS_SNAPSHOT", "dns.snapshots.list", domain))
	add(a.liveMail(ctx, client))

	for _, r := range a.sshChecks(ctx, acc) {
		add(r)
	}

	sort.Slice(rep.Results, func(i, j int) bool { return rep.Results[i].Code < rep.Results[j].Code })
	if err := a.persist(ctx, rep); err != nil {
		return rep, err
	}
	return rep, nil
}

func (a *Auditor) contractOnly(c contractCheck) Result {
	ep, err := a.Registry.Lookup(c.endpoint)
	if err != nil {
		return Result{Code: c.code, Status: Unknown, Note: "Endpoint tidak ada di contract registry: " + err.Error()}
	}
	return Result{
		Code:   c.code,
		Status: Unknown,
		Note:   fmt.Sprintf("Endpoint %s %s ada di kontrak (%s), belum diuji nyata; diuji di Fase %d. %s", ep.Method, ep.Path, ep.ID, c.phase, c.note),
		Detail: map[string]any{"endpoint": ep.ID, "diuji_fase": c.phase},
	}
}

func pickDomain(sites []hostinger.Website) string {
	for _, s := range sites {
		if s.VhostType == "main" && s.Domain != "" {
			return s.Domain
		}
	}
	for _, s := range sites {
		if s.Domain != "" && s.VhostType != "subdomain" {
			return s.Domain
		}
	}
	return ""
}

func (a *Auditor) liveDatabases(ctx context.Context, client *hostinger.Client, accountID string) Result {
	type db struct {
		Name string `json:"name"`
	}
	dbs, err := hostinger.ListAll[db](ctx, client, hostinger.Request{Endpoint: "hosting.databases.list", PathParams: map[string]string{"username": accountID}}, 100)
	if err != nil {
		return Result{Code: "API_DB_LIST", Status: Unknown, Note: "Gagal dibaca: " + err.Error()}
	}
	return Result{Code: "API_DB_LIST", Status: Supported, Note: fmt.Sprintf("Teruji: %d database terbaca.", len(dbs)), Detail: map[string]any{"jumlah_database": len(dbs)}}
}

func (a *Auditor) liveDNS(ctx context.Context, client *hostinger.Client, code, endpoint, domain string) Result {
	if domain == "" {
		return Result{Code: code, Status: Unknown, Note: "Tidak ada domain utama di akun ini untuk diuji."}
	}
	var out []json.RawMessage
	_, err := client.Do(ctx, hostinger.Request{Endpoint: endpoint, PathParams: map[string]string{"domain": domain}}, &out)
	if err != nil {
		return Result{Code: code, Status: Unknown, Note: fmt.Sprintf("Zone %s tidak bisa dibaca dengan token ini (%v). Kemungkinan DNS dikelola di akun/registrar lain; dicek ulang per domain di Fase 3.", domain, err)}
	}
	label := "record"
	if code == "API_DNS_SNAPSHOT" {
		label = "snapshot"
	}
	return Result{Code: code, Status: Supported, Note: fmt.Sprintf("Teruji pada %s: %d %s terbaca.", domain, len(out), label), Detail: map[string]any{"domain": domain, "jumlah": len(out)}}
}

func (a *Auditor) liveMail(ctx context.Context, client *hostinger.Client) Result {
	type order struct {
		ID string `json:"id"`
	}
	orders, err := hostinger.ListAll[order](ctx, client, hostinger.Request{Endpoint: "mail.orders.list"}, 100)
	if err != nil {
		return Result{Code: "API_EMAIL_INVENTORY", Status: Unknown, Note: "Gagal dibaca: " + err.Error() + ". Kemungkinan Free Business Email tidak tersedia lewat /api/mail/v1 (R9)."}
	}
	note := fmt.Sprintf("Teruji: %d order email terbaca di token ini.", len(orders))
	if len(orders) == 0 {
		note += " Kosong bisa berarti tidak ada layanan email, atau Free Business Email tidak muncul di endpoint ini (R9)."
	}
	return Result{Code: "API_EMAIL_INVENTORY", Status: Supported, Note: note, Detail: map[string]any{"jumlah_order": len(orders)}}
}

const toolsCommand = `for c in tar gzip mysqldump mysql sha256sum wp; do if command -v "$c" >/dev/null 2>&1; then echo "$c=ada"; else echo "$c=tidak"; fi; done`

const quotaCommand = `df -P -B1 "$HOME" | tail -n 1; df -P -i "$HOME" | tail -n 1`

var requiredTools = []string{"tar", "gzip", "mysqldump", "mysql", "sha256sum"}

func (a *Auditor) sshChecks(ctx context.Context, acc store.HostingAccount) []Result {
	info, err := a.Store.GetSSH(ctx, acc.ID)
	if err != nil {
		return []Result{{Code: "SSH", Status: Unknown, Note: err.Error()}, {Code: "SSH_TOOLS", Status: Unknown}, {Code: "QUOTA", Status: Unknown}}
	}
	quotaNote := "Kuota website, disk, dan inode tidak tersedia di API Shared Hosting; dikonfirmasi operator di Fase 3 (D-011)."
	switch info.Status {
	case store.SSHNotConfigured:
		return []Result{
			{Code: "SSH", Status: Unknown, Note: "SSH belum diatur. Jalankan 'migrator ssh setup " + acc.ID + "'."},
			{Code: "SSH_TOOLS", Status: Unknown, Note: "Menunggu SSH."},
			{Code: "QUOTA", Status: Unknown, Note: quotaNote},
		}
	case store.SSHBlocked:
		return []Result{
			{Code: "SSH", Status: Unknown, Note: "SSH BLOCKED: " + info.LastError},
			{Code: "SSH_TOOLS", Status: Unknown, Note: "Menunggu SSH."},
			{Code: "QUOTA", Status: Unknown, Note: quotaNote},
		}
	case store.SSHFailed:
		return []Result{
			{Code: "SSH", Status: Manual, Note: "SSH gagal: " + info.LastError + " Fallback: checkpoint MANUAL_BACKUP (unduh backup dari hPanel)."},
			{Code: "SSH_TOOLS", Status: Unknown, Note: "Menunggu SSH."},
			{Code: "QUOTA", Status: Unknown, Note: quotaNote},
		}
	}
	client, err := a.SSH.Connect(ctx, acc.ID)
	if err != nil {
		status := Manual
		note := "Koneksi SSH gagal saat audit: " + err.Error() + " Fallback: checkpoint MANUAL_BACKUP."
		if errors.Is(err, sshx.ErrBlocked) {
			status = Unknown
			note = "SSH BLOCKED karena fingerprint berubah. Periksa lalu jalankan 'migrator ssh setup " + acc.ID + " --terima-host-baru'."
		} else {
			_, msg := sshx.Classify(err)
			note = msg + " Fallback: checkpoint MANUAL_BACKUP."
		}
		return []Result{{Code: "SSH", Status: status, Note: note}, {Code: "SSH_TOOLS", Status: Unknown, Note: "Menunggu SSH."}, {Code: "QUOTA", Status: Unknown, Note: quotaNote}}
	}
	defer client.Close()
	results := []Result{}
	if dir, err := client.CheckSFTP(); err != nil {
		results = append(results, Result{Code: "SSH", Status: Manual, Note: "SSH login berhasil tetapi SFTP gagal: " + err.Error()})
	} else {
		results = append(results, Result{Code: "SSH", Status: Supported, Note: fmt.Sprintf("Teruji: login %s (%s) dan SFTP berhasil, folder kerja %s.", info.User, info.Auth, dir), Detail: map[string]any{"host": info.Host, "port": info.Port, "auth": info.Auth}})
	}
	results = append(results, a.toolsCheck(ctx, client))
	results = append(results, a.quotaCheck(ctx, client, acc.Plan, quotaNote))
	return results
}

func (a *Auditor) runCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	wait := a.CommandWait
	if wait <= 0 {
		wait = 60 * time.Second
	}
	return context.WithTimeout(ctx, wait)
}

func (a *Auditor) toolsCheck(ctx context.Context, client *sshx.Client) Result {
	cctx, cancel := a.runCtx(ctx)
	defer cancel()
	out, err := client.Run(cctx, toolsCommand, nil)
	if err != nil {
		return Result{Code: "SSH_TOOLS", Status: Unknown, Note: "Gagal memeriksa tools: " + err.Error()}
	}
	found := map[string]bool{}
	for _, line := range strings.Split(out.Stdout, "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			found[k] = v == "ada"
		}
	}
	var missing []string
	for _, t := range requiredTools {
		if !found[t] {
			missing = append(missing, t)
		}
	}
	detail := map[string]any{}
	for k, v := range found {
		detail[k] = v
	}
	wpNote := "wp-cli tersedia."
	if !found["wp"] {
		wpNote = "wp-cli tidak ada; WordPress memakai metode verifikasi utama (header Host) dan search-replace via PHP bila perlu."
	}
	if len(missing) > 0 {
		return Result{Code: "SSH_TOOLS", Status: Manual, Note: "Tools wajib tidak ditemukan: " + strings.Join(missing, ", ") + ". Fallback: checkpoint MANUAL_BACKUP. " + wpNote, Detail: detail}
	}
	return Result{Code: "SSH_TOOLS", Status: Supported, Note: "Teruji: tar, gzip, mysqldump, mysql, sha256sum tersedia. " + wpNote, Detail: detail}
}

func (a *Auditor) quotaCheck(ctx context.Context, client *sshx.Client, plan, quotaNote string) Result {
	cctx, cancel := a.runCtx(ctx)
	defer cancel()
	out, err := client.Run(cctx, quotaCommand, nil)
	if err != nil || out.ExitCode != 0 {
		return Result{Code: "QUOTA", Status: Unknown, Note: "Gagal membaca df. " + quotaNote}
	}
	lines := strings.Split(strings.TrimSpace(out.Stdout), "\n")
	detail := map[string]any{"paket": plan}
	var parts []string
	if len(lines) >= 1 {
		if f := strings.Fields(lines[0]); len(f) >= 4 {
			total, _ := strconv.ParseInt(f[1], 10, 64)
			used, _ := strconv.ParseInt(f[2], 10, 64)
			avail, _ := strconv.ParseInt(f[3], 10, 64)
			detail["disk_total_byte"] = total
			detail["disk_terpakai_byte"] = used
			detail["disk_sisa_byte"] = avail
			parts = append(parts, fmt.Sprintf("disk terpakai %s dari %s (sisa %s)", HumanBytes(used), HumanBytes(total), HumanBytes(avail)))
		}
	}
	if len(lines) >= 2 {
		if f := strings.Fields(lines[1]); len(f) >= 4 {
			total, _ := strconv.ParseInt(f[1], 10, 64)
			used, _ := strconv.ParseInt(f[2], 10, 64)
			detail["inode_total"] = total
			detail["inode_terpakai"] = used
			parts = append(parts, fmt.Sprintf("inode %d dari %d", used, total))
		}
	}
	note := "Nilai df: " + strings.Join(parts, ", ") + ". Di shared hosting angka df belum tentu sama dengan kuota paket. " + quotaNote
	if len(parts) == 0 {
		note = "Output df tidak terbaca. " + quotaNote
	}
	return Result{Code: "QUOTA", Status: Unknown, Note: note, Detail: detail}
}

func HumanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return strings.Replace(fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp]), ".", ",", 1)
}

func (a *Auditor) persist(ctx context.Context, rep Report) error {
	caps := make([]store.Capability, 0, len(rep.Results))
	for _, r := range rep.Results {
		var detail string
		if len(r.Detail) > 0 {
			b, _ := json.Marshal(r.Detail)
			detail = string(b)
		}
		caps = append(caps, store.Capability{Code: r.Code, Status: r.Status, Note: r.Note, DetailJSON: detail})
	}
	if err := a.Store.SaveCapabilities(ctx, rep.AccountID, caps); err != nil {
		return err
	}
	summary := fmt.Sprintf("SUPPORTED %d, MANUAL %d, UNSUPPORTED %d, UNKNOWN %d", rep.Count(Supported), rep.Count(Manual), rep.Count(Unsupported), rep.Count(Unknown))
	if a.Log != nil {
		a.Log.Info("audit kapabilitas", "akun", rep.AccountID, "alias", rep.Alias, "ringkasan", summary)
	}
	return a.Store.AppendAudit(ctx, nil, "operator", "audit.run", rep.AccountID, "OK", "alias "+rep.Alias+": "+summary)
}
