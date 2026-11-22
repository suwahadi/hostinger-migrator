package report

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"hostinger-migrator/internal/backup"
	"hostinger-migrator/internal/email"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/store"
)

type Summary struct {
	Total     int `json:"total"`
	Selesai   int `json:"selesai"`
	DummyOnly int `json:"dummy_only"`
	Proses    int `json:"dalam_proses"`
	Manual    int `json:"menunggu_manual"`
	Tertahan  int `json:"tertahan"`
	Gagal     int `json:"gagal"`
}

type WebsiteRow struct {
	Domain      string `json:"domain_asal"`
	Tujuan      string `json:"tujuan"`
	Dummy       string `json:"domain_dummy"`
	Tipe        string `json:"tipe"`
	Ukuran      string `json:"ukuran"`
	File        string `json:"status_file"`
	Database    string `json:"status_database"`
	Verifikasi  string `json:"status_verifikasi"`
	DNS         string `json:"status_dns"`
	SSL         string `json:"status_ssl"`
	Email       string `json:"status_email"`
	Status      string `json:"status"`
	Kesimpulan  string `json:"kesimpulan"`
	Catatan     string `json:"catatan"`
	MetodeVerif string `json:"metode_verifikasi"`
}

type EmailRow struct {
	Domain  string `json:"domain"`
	Mailbox string `json:"mailbox"`
	Sumber  int    `json:"pesan_sumber"`
	Tujuan  string `json:"pesan_tujuan"`
	Status  string `json:"status"`
}

type DNSRow struct {
	Domain  string `json:"domain"`
	Sebelum string `json:"sebelum"`
	Sesudah string `json:"sesudah"`
}

type PendingRow struct {
	Domain    string `json:"domain"`
	Kode      string `json:"kode"`
	Instruksi string `json:"instruksi"`
	Sejak     string `json:"sejak"`
}

type SSHRow struct {
	Akun   string `json:"akun"`
	Alias  string `json:"token"`
	Status string `json:"status_ssh"`
	Login  string `json:"login"`
}

type ErrorRow struct {
	Domain string `json:"domain"`
	Error  string `json:"error"`
	Saran  string `json:"saran"`
}

type Report struct {
	BatchID      string       `json:"batch"`
	Sumber       string       `json:"sumber"`
	Tujuan       string       `json:"tujuan"`
	StatusBatch  string       `json:"status_batch"`
	Dibuat       string       `json:"dibuat"`
	Mulai        string       `json:"mulai"`
	Selesai      string       `json:"selesai"`
	Ringkasan    Summary      `json:"ringkasan"`
	Website      []WebsiteRow `json:"website"`
	DummyOnly    []WebsiteRow `json:"website_dummy_only"`
	Email        []EmailRow   `json:"email"`
	DNS          []DNSRow     `json:"dns"`
	Tertunda     []PendingRow `json:"tindakan_manual_tertunda"`
	LokasiBackup string       `json:"lokasi_backup"`
	SSHKey       []SSHRow     `json:"akun_dengan_ssh_key_migrator"`
	Error        []ErrorRow   `json:"error"`
	Versi        string       `json:"versi_migrator"`
}

type Builder struct {
	Store     *store.Store
	BackupDir string
	Redactor  *logx.Redactor
	Version   string
	Now       func() time.Time
}

func (b *Builder) red(s string) string {
	if b.Redactor == nil {
		return logx.Global.String(s)
	}
	return b.Redactor.String(s)
}

func human(n int64) string {
	if n < 0 {
		return "?"
	}
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

func ver(vs []store.Verification, name string) (string, string) {
	for _, v := range vs {
		if v.CheckType == name {
			return v.Result, v.Detail
		}
	}
	return "", ""
}

type cutoverCP struct {
	DNSChanged []string `json:"dns_diubah"`
}

func (b *Builder) Build(ctx context.Context, batchID string) (*Report, error) {
	bt, items, err := b.Store.GetBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}
	now := time.Now
	if b.Now != nil {
		now = b.Now
	}
	full, _ := b.Store.ListItems(ctx, batchID)
	rep := &Report{BatchID: bt.ID, Sumber: bt.SourceAlias + " / " + bt.SourceAccount, Tujuan: bt.DestAlias + " / " + bt.DestAccount, StatusBatch: bt.Status,
		Dibuat: now().UTC().Format(time.RFC3339), LokasiBackup: filepath.Join(b.BackupDir, bt.ID), Versi: b.Version}
	_ = items
	for _, it := range full {
		if rep.Mulai == "" || (it.StartedAt != "" && it.StartedAt < rep.Mulai) {
			rep.Mulai = it.StartedAt
		}
		if it.FinishedAt > rep.Selesai {
			rep.Selesai = it.FinishedAt
		}
		row := b.website(ctx, it)
		rep.Ringkasan.Total++
		switch {
		case it.State == pipeline.StateCompleted:
			rep.Ringkasan.Selesai++
		case it.State == pipeline.StateDummyOnly:
			rep.Ringkasan.DummyOnly++
		case it.SideState == store.SideFailed:
			rep.Ringkasan.Gagal++
		case it.SideState == store.SideWaitingManual:
			rep.Ringkasan.Manual++
		case it.SideState == store.SideBlocked || it.State == pipeline.StateBlocked:
			rep.Ringkasan.Tertahan++
		default:
			rep.Ringkasan.Proses++
		}
		rep.Website = append(rep.Website, row)
		if it.State == pipeline.StateDummyOnly {
			rep.DummyOnly = append(rep.DummyOnly, row)
		}
		rep.Email = append(rep.Email, b.emails(ctx, it)...)
		if d := b.dns(ctx, it); d != nil {
			rep.DNS = append(rep.DNS, *d)
		}
		if it.LastError != "" && (it.SideState != "" || it.RetryCount > 0) {
			rep.Error = append(rep.Error, ErrorRow{Domain: it.SourceDomain, Error: b.red(it.LastError), Saran: Suggest(it.LastError, it.ManualCode)})
		}
	}
	tasks, _ := b.Store.ListManualTasks(ctx, true)
	for _, t := range tasks {
		if t.BatchID == batchID {
			rep.Tertunda = append(rep.Tertunda, PendingRow{Domain: t.Domain, Kode: t.Code, Instruksi: b.red(t.Instructions), Sejak: t.CreatedAt})
		}
	}
	infos, _ := b.Store.ListSSH(ctx)
	for _, i := range infos {
		if i.Host == "" {
			continue
		}
		login := "SSH key"
		if i.Auth == store.SSHAuthPassword {
			login = "password"
		}
		rep.SSHKey = append(rep.SSHKey, SSHRow{Akun: i.AccountID, Alias: strings.Join(i.Aliases, ", "), Status: i.Status, Login: login})
	}
	return rep, nil
}

func (b *Builder) website(ctx context.Context, it *store.PipelineItem) WebsiteRow {
	row := WebsiteRow{Domain: it.SourceDomain, Dummy: it.DummyDomain, Status: pipeline.ItemLabel(it), File: "-", Database: "-", Verifikasi: "-", DNS: "-", SSL: "-", Email: "-"}
	if it.WebsiteID > 0 {
		if w, err := b.Store.GetWebsiteByID(ctx, it.WebsiteID); err == nil {
			row.Tipe = w.SiteType
			row.Ukuran = human(w.SizeBytes)
		}
	}
	arts, _ := b.Store.ListArtifacts(ctx, it.ID)
	for _, a := range arts {
		if (a.Kind == backup.KindFiles || a.Kind == backup.FinalPrefix+backup.KindFiles) && a.Validated {
			row.File = "OK (" + human(a.Size) + ")"
		}
	}
	if len(it.DestDBs) == 0 {
		row.Database = "tidak ada DB"
	} else {
		ok := 0
		for _, d := range it.DestDBs {
			if d.Imported {
				ok++
			}
		}
		row.Database = fmt.Sprintf("%d/%d diimport, jumlah baris cocok", ok, len(it.DestDBs))
		if ok < len(it.DestDBs) {
			row.Database = fmt.Sprintf("%d/%d diimport", ok, len(it.DestDBs))
		}
	}
	vs, _ := b.Store.ListVerifications(ctx, it.ID)
	if r, _ := ver(vs, "VERIFIKASI_HASIL"); r != "" {
		row.Verifikasi = r
	}
	_, row.MetodeVerif = ver(vs, "VERIFIKASI_METODE")
	if r, d := ver(vs, "SSL_STATUS"); r != "" {
		row.SSL = r + " · " + d
	}
	steps, _ := b.Store.ListSteps(ctx, it.ID)
	for _, st := range steps {
		if st.Name == "cutover" && st.Checkpoint != "" {
			var cp cutoverCP
			json.Unmarshal([]byte(st.Checkpoint), &cp)
			if len(cp.DNSChanged) > 0 {
				row.DNS = strings.Join(cp.DNSChanged, "; ")
			}
		}
	}
	boxes, _ := b.Store.ListMailboxes(ctx, []string{it.SourceDomain})
	switch {
	case len(boxes) == 0:
		row.Email = "tidak ada email"
	case it.EmailState == "SELESAI":
		row.Email = fmt.Sprintf("selesai (%d mailbox)", len(boxes))
	case it.EmailState == "":
		row.Email = fmt.Sprintf("belum dipindah (%d mailbox)", len(boxes))
	default:
		row.Email = "proses: " + it.EmailState
	}
	row.Tujuan = it.DummyDomain
	if row.DNS != "-" {
		row.Tujuan = it.SourceDomain
	}
	row.Kesimpulan = conclusion(it, row, len(boxes))
	var notes []string
	if it.PlanNotes != "" {
		notes = append(notes, it.PlanNotes)
	}
	if it.LastError != "" {
		notes = append(notes, b.red(it.LastError))
	}
	row.Catatan = strings.Join(notes, " · ")
	return row
}

func conclusion(it *store.PipelineItem, row WebsiteRow, boxes int) string {
	switch {
	case it.State == pipeline.StateDummyOnly:
		return "Hanya dummy (domain mati) → " + it.DummyDomain
	case it.State == pipeline.StateBlocked || it.SideState == store.SideBlocked:
		return "Tertahan: " + it.LastError
	case it.SideState == store.SideFailed:
		return "Gagal setelah percobaan habis: " + it.LastError
	case it.SideState == store.SideWaitingManual:
		return "Menunggu tindakan manual " + it.ManualCode
	}
	var missing []string
	if row.DNS == "-" {
		missing = append(missing, "DNS belum dipindah")
	}
	if !strings.HasPrefix(row.SSL, "PASS") {
		missing = append(missing, "SSL belum aktif")
	}
	if boxes > 0 && it.EmailState != "SELESAI" {
		missing = append(missing, "email belum dipindah")
	}
	if it.State == pipeline.StateCompleted && len(missing) == 0 {
		return "Selesai (sumber sudah dibersihkan)"
	}
	if it.State == pipeline.StateCleanupReady && len(missing) == 0 {
		return "Selesai di tujuan, menunggu cleanup sumber"
	}
	if it.State == pipeline.StateCompleted {
		return "Sumber dibersihkan, catatan: " + strings.Join(missing, ", ")
	}
	return "Belum selesai (" + it.State + "): " + strings.Join(missing, ", ")
}

func (b *Builder) emails(ctx context.Context, it *store.PipelineItem) []EmailRow {
	boxes, _ := b.Store.ListMailboxes(ctx, []string{it.SourceDomain})
	if len(boxes) == 0 {
		return nil
	}
	mans, _ := email.Manifests(b.BackupDir, it)
	backed := map[string]int{}
	for _, m := range mans {
		backed[m.Address] = m.Total()
	}
	vs, _ := b.Store.ListVerifications(ctx, it.ID)
	var out []EmailRow
	for _, m := range boxes {
		row := EmailRow{Domain: it.SourceDomain, Mailbox: m.Address, Sumber: int(m.MessageCount), Tujuan: "-", Status: "belum dipindah"}
		if n, ok := backed[m.Address]; ok {
			row.Sumber = n
			row.Status = "backup selesai"
		}
		var details []string
		failed := false
		for _, v := range vs {
			if strings.HasPrefix(v.CheckType, "EMAIL_FOLDER_"+m.Address+"/") {
				details = append(details, v.Detail)
				if v.Result == "FAIL" {
					failed = true
				}
			}
		}
		if len(details) > 0 {
			row.Tujuan = strings.Join(details, "; ")
			row.Status = "cocok"
			if failed {
				row.Status = "TIDAK COCOK"
			}
		}
		out = append(out, row)
	}
	return out
}

func (b *Builder) dns(ctx context.Context, it *store.PipelineItem) *DNSRow {
	snaps, _ := b.Store.ListDNSSnapshots(ctx, it.SourceDomain)
	var snap *store.DNSSnapshot
	for i := range snaps {
		if snaps[i].ItemID == it.ID {
			snap = &snaps[i]
		}
	}
	if snap == nil {
		return nil
	}
	var recs []struct {
		Name    string `json:"name"`
		Type    string `json:"type"`
		Records []struct {
			Content string `json:"content"`
		} `json:"records"`
	}
	json.Unmarshal([]byte(snap.RecordsJSON), &recs)
	var before []string
	for _, r := range recs {
		var vals []string
		for _, x := range r.Records {
			vals = append(vals, x.Content)
		}
		before = append(before, fmt.Sprintf("%s %s → %s", r.Type, r.Name, strings.Join(vals, ", ")))
	}
	sort.Strings(before)
	row := &DNSRow{Domain: it.SourceDomain, Sebelum: strings.Join(before, "; "), Sesudah: "belum diubah"}
	steps, _ := b.Store.ListSteps(ctx, it.ID)
	for _, st := range steps {
		if st.Name == "cutover" && st.Checkpoint != "" {
			var cp cutoverCP
			json.Unmarshal([]byte(st.Checkpoint), &cp)
			if len(cp.DNSChanged) > 0 {
				row.Sesudah = strings.Join(cp.DNSChanged, "; ") + " (record lain tidak diubah)"
			}
		}
	}
	return row
}

func Suggest(errMsg, code string) string {
	l := strings.ToLower(errMsg)
	switch {
	case code != "":
		return "Kerjakan langkah di 'migrator pending', lalu 'migrator confirm " + code + " <domain>'"
	case strings.Contains(l, "token ditolak"):
		return "Buat API token baru di hPanel, 'migrator account remove <alias>' lalu 'migrator account add', kemudian 'migrator retry --failed --termasuk-blocked'"
	case strings.Contains(l, "fingerprint"):
		return "Periksa server SSH di hPanel, lalu 'migrator ssh setup <akun> --terima-host-baru'"
	case strings.Contains(l, "ssh"):
		return "Jalankan 'migrator ssh test' dan perbaiki akses SSH, lalu 'migrator retry --failed'"
	case strings.Contains(l, "jumlah baris"), strings.Contains(l, "import"):
		return "Periksa kuota dan versi MySQL tujuan, lalu 'migrator retry --item <domain>'"
	case strings.Contains(l, "verifikasi"):
		return "Lihat 'migrator status --item <domain>' untuk detail pemeriksaan yang gagal"
	case strings.Contains(l, "dns"):
		return "Periksa propagasi DNS dan zone domain"
	}
	return "Lihat log di folder data/logs, lalu 'migrator retry --item <domain>'"
}

func (b *Builder) Write(rep *Report, dir string, formats map[string]bool) ([]string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	var out []string
	if formats["json"] {
		data, _ := json.MarshalIndent(rep, "", "  ")
		p := filepath.Join(dir, "laporan.json")
		if err := os.WriteFile(p, []byte(b.red(string(data))), 0o600); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	if formats["csv"] {
		p := filepath.Join(dir, "laporan.csv")
		f, err := os.Create(p)
		if err != nil {
			return out, err
		}
		w := csv.NewWriter(f)
		w.Write([]string{"domain_asal", "tujuan", "dummy", "tipe", "ukuran", "file", "database", "verifikasi", "dns", "ssl", "email", "status", "kesimpulan", "catatan"})
		for _, r := range rep.Website {
			w.Write([]string{r.Domain, r.Tujuan, r.Dummy, r.Tipe, r.Ukuran, r.File, r.Database, r.Verifikasi, r.DNS, r.SSL, r.Email, r.Status, r.Kesimpulan, b.red(r.Catatan)})
		}
		w.Flush()
		f.Close()
		if err := w.Error(); err != nil {
			return out, err
		}
		out = append(out, p)
	}
	if formats["html"] {
		p := filepath.Join(dir, "laporan.html")
		f, err := os.Create(p)
		if err != nil {
			return out, err
		}
		err = htmlTemplate.Execute(f, rep)
		f.Close()
		if err != nil {
			return out, err
		}
		out = append(out, p)
	}
	return out, nil
}

var htmlTemplate = template.Must(template.New("laporan").Parse(`<!DOCTYPE html>
<html lang="id"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>Laporan Migrasi {{.BatchID}}</title>
<style>
body{font-family:system-ui,Segoe UI,Arial,sans-serif;margin:24px;color:#1f2933;background:#fff}
h1{font-size:22px}h2{font-size:17px;margin-top:28px;border-bottom:1px solid #d9e2ec;padding-bottom:4px}
table{border-collapse:collapse;width:100%;font-size:13px;margin-top:8px}th,td{border:1px solid #d9e2ec;padding:6px 8px;text-align:left;vertical-align:top}
th{background:#f0f4f8}.ok{color:#18794e}.warn{color:#a15c07}.bad{color:#c62828}.kartu{display:inline-block;margin:4px 12px 4px 0;padding:8px 12px;border:1px solid #d9e2ec;border-radius:6px}
pre{white-space:pre-wrap;margin:0;font-family:inherit}.ingat{background:#fff8e1;border:1px solid #f0c36d;padding:10px;border-radius:6px}
</style></head><body>
<h1>Laporan Migrasi {{.BatchID}}</h1>
<p>Sumber: <b>{{.Sumber}}</b> · Tujuan: <b>{{.Tujuan}}</b> · Status batch: <b>{{.StatusBatch}}</b><br>
Mulai: {{.Mulai}} · Selesai: {{if .Selesai}}{{.Selesai}}{{else}}-{{end}} · Laporan dibuat: {{.Dibuat}} · Versi: {{.Versi}}</p>
<div><span class="kartu">Total: {{.Ringkasan.Total}}</span><span class="kartu ok">Selesai: {{.Ringkasan.Selesai}}</span><span class="kartu">Dummy only: {{.Ringkasan.DummyOnly}}</span><span class="kartu">Dalam proses: {{.Ringkasan.Proses}}</span><span class="kartu warn">Menunggu manual: {{.Ringkasan.Manual}}</span><span class="kartu warn">Tertahan: {{.Ringkasan.Tertahan}}</span><span class="kartu bad">Gagal: {{.Ringkasan.Gagal}}</span></div>
<h2>Website</h2>
<table><tr><th>Domain asal</th><th>Tujuan</th><th>Tipe</th><th>Ukuran</th><th>File</th><th>Database</th><th>Verifikasi</th><th>DNS</th><th>SSL</th><th>Email</th><th>Kesimpulan</th><th>Catatan</th></tr>
{{range .Website}}<tr><td>{{.Domain}}</td><td>{{.Tujuan}}</td><td>{{.Tipe}}</td><td>{{.Ukuran}}</td><td>{{.File}}</td><td>{{.Database}}</td><td>{{.Verifikasi}}<br><small>{{.MetodeVerif}}</small></td><td>{{.DNS}}</td><td>{{.SSL}}</td><td>{{.Email}}</td><td><b>{{.Kesimpulan}}</b></td><td>{{.Catatan}}</td></tr>
{{end}}</table>
<h2>Website DUMMY_ONLY</h2>
{{if .DummyOnly}}<table><tr><th>Domain (mati)</th><th>URL dummy</th></tr>{{range .DummyOnly}}<tr><td>{{.Domain}}</td><td>{{.Dummy}}</td></tr>{{end}}</table>{{else}}<p>Tidak ada.</p>{{end}}
<h2>Email</h2>
{{if .Email}}<table><tr><th>Domain</th><th>Mailbox</th><th>Pesan sumber</th><th>Pesan tujuan</th><th>Status</th></tr>{{range .Email}}<tr><td>{{.Domain}}</td><td>{{.Mailbox}}</td><td>{{.Sumber}}</td><td>{{.Tujuan}}</td><td>{{.Status}}</td></tr>{{end}}</table>{{else}}<p>Tidak ada email.</p>{{end}}
<h2>DNS sebelum dan sesudah</h2>
{{if .DNS}}<table><tr><th>Domain</th><th>Sebelum</th><th>Sesudah</th></tr>{{range .DNS}}<tr><td>{{.Domain}}</td><td>{{.Sebelum}}</td><td>{{.Sesudah}}</td></tr>{{end}}</table>{{else}}<p>Belum ada perubahan DNS.</p>{{end}}
<h2>Tindakan manual tertunda</h2>
{{if .Tertunda}}<table><tr><th>Domain</th><th>Kode</th><th>Instruksi</th><th>Sejak</th></tr>{{range .Tertunda}}<tr><td>{{.Domain}}</td><td>{{.Kode}}</td><td><pre>{{.Instruksi}}</pre></td><td>{{.Sejak}}</td></tr>{{end}}</table>{{else}}<p>Tidak ada.</p>{{end}}
<h2>Lokasi backup lokal</h2><p>{{.LokasiBackup}}</p>
<h2>Akun yang masih memasang SSH key migrator</h2>
<div class="ingat">Setelah migrasi selesai, hapus SSH key <b>hostinger-migrator</b> di hPanel → Advanced → SSH Access pada akun berikut, lalu hapus password SSH cadangan dengan <code>migrator ssh remove-password &lt;akun&gt;</code>.</div>
{{if .SSHKey}}<table><tr><th>Akun</th><th>Token</th><th>Status SSH</th><th>Login</th></tr>{{range .SSHKey}}<tr><td>{{.Akun}}</td><td>{{.Alias}}</td><td>{{.Status}}</td><td>{{.Login}}</td></tr>{{end}}</table>{{else}}<p>Tidak ada.</p>{{end}}
<h2>Error dan saran tindakan</h2>
{{if .Error}}<table><tr><th>Domain</th><th>Error</th><th>Saran</th></tr>{{range .Error}}<tr><td>{{.Domain}}</td><td>{{.Error}}</td><td>{{.Saran}}</td></tr>{{end}}</table>{{else}}<p>Tidak ada error.</p>{{end}}
</body></html>
`))

func (rep *Report) Terminal() string {
	var b strings.Builder
	fmt.Fprintf(&b, "LAPORAN %s · %s → %s · %s\n", rep.BatchID, rep.Sumber, rep.Tujuan, rep.StatusBatch)
	s := rep.Ringkasan
	fmt.Fprintf(&b, "Total %d · Selesai %d · Dummy only %d · Proses %d · Manual %d · Tertahan %d · Gagal %d\n", s.Total, s.Selesai, s.DummyOnly, s.Proses, s.Manual, s.Tertahan, s.Gagal)
	for _, w := range rep.Website {
		fmt.Fprintf(&b, "  %-28s %s\n", w.Domain, w.Kesimpulan)
	}
	if len(rep.Tertunda) > 0 {
		fmt.Fprintf(&b, "Tindakan manual tertunda: %d (lihat 'migrator pending')\n", len(rep.Tertunda))
	}
	if len(rep.SSHKey) > 0 {
		var accs []string
		for _, a := range rep.SSHKey {
			accs = append(accs, a.Akun)
		}
		fmt.Fprintf(&b, "PENGINGAT: hapus SSH key migrator di hPanel akun: %s\n", strings.Join(accs, ", "))
	}
	return b.String()
}
