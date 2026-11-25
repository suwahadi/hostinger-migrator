package inventory

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"hostinger-migrator/internal/detector"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

var ErrTokenRejected = errors.New("token ditolak Hostinger saat inventaris")

type Scanner struct {
	Store        *store.Store
	NewClient    func(alias string) (*hostinger.Client, error)
	SSH          *sshx.Service
	DataDir      string
	Redactor     *logx.Redactor
	Log          *slog.Logger
	LookupNS     func(ctx context.Context, domain string) ([]string, error)
	ProbeTimeout time.Duration
	Parallel     int
	SkipSize     bool
	Progress     func(Progress)
}

const DefaultParallel = 4

type Progress struct {
	AccountID string
	Stage     string
	Done      int
	Total     int
	Domain    string
	Type      string
	SizeBytes int64
	Elapsed   time.Duration
	Warnings  int
}

const (
	StageAccount = "akun"
	StageWebsite = "website"
)

type siteResult struct {
	website   store.Website
	warnings  []string
	refs      []detector.DBRef
	mailboxes []store.MailboxRow
}

type aliasCache struct {
	portfolio    map[string]hostinger.PortfolioDomain
	portfolioErr error
	mailOrders   map[string]hostinger.MailOrder
	mailErr      error
}

func (s *Scanner) redactor() *logx.Redactor {
	if s.Redactor == nil {
		return logx.Global
	}
	return s.Redactor
}

func (s *Scanner) ScanAlias(ctx context.Context, alias string) ([]*store.InventorySnapshot, error) {
	return s.ScanAccounts(ctx, alias, nil)
}

func (s *Scanner) ScanAccounts(ctx context.Context, alias string, only []string) ([]*store.InventorySnapshot, error) {
	all, err := s.Store.ListHostingAccounts(ctx, alias)
	if err != nil {
		return nil, err
	}
	if len(all) == 0 {
		return nil, fmt.Errorf("token %s belum memiliki akun hosting tercatat; jalankan 'migrator account test %s'", alias, alias)
	}
	accounts := all
	if len(only) > 0 {
		want := map[string]bool{}
		for _, id := range only {
			want[id] = true
		}
		accounts = nil
		for _, acc := range all {
			if want[acc.ID] {
				accounts = append(accounts, acc)
			}
		}
		if len(accounts) != len(want) {
			return nil, fmt.Errorf("sebagian akun yang diminta tidak terdaftar di token %s: %s", alias, strings.Join(only, ", "))
		}
	}
	client, err := s.NewClient(alias)
	if err != nil {
		return nil, err
	}
	client = client.ReadOnlyCopy()
	cache := &aliasCache{}
	var out []*store.InventorySnapshot
	for _, acc := range accounts {
		snap, err := s.ScanAccount(ctx, client, cache, alias, acc)
		if err != nil {
			return out, err
		}
		out = append(out, snap)
	}
	return out, nil
}

func (s *Scanner) loadAliasData(ctx context.Context, client *hostinger.Client, cache *aliasCache) {
	if cache.portfolio == nil && cache.portfolioErr == nil {
		list, err := client.ListPortfolio(ctx)
		cache.portfolioErr = err
		cache.portfolio = map[string]hostinger.PortfolioDomain{}
		for _, d := range list {
			if d.Domain != "" {
				cache.portfolio[strings.ToLower(d.Domain)] = d
			}
		}
	}
	if cache.mailOrders == nil && cache.mailErr == nil {
		orders, err := client.ListMailOrders(ctx)
		cache.mailErr = err
		cache.mailOrders = map[string]hostinger.MailOrder{}
		for _, o := range orders {
			if d := o.DomainName(); d != "" {
				cache.mailOrders[strings.ToLower(d)] = o
			}
		}
	}
}

func (s *Scanner) ScanAccount(ctx context.Context, client *hostinger.Client, cache *aliasCache, alias string, acc store.HostingAccount) (*store.InventorySnapshot, error) {
	if !client.IsReadOnly() {
		client = client.ReadOnlyCopy()
	}
	if cache == nil {
		cache = &aliasCache{}
	}
	snap := &store.InventorySnapshot{AccountID: acc.ID, Alias: alias}
	warn := func(format string, args ...any) {
		snap.Warnings = append(snap.Warnings, s.redactor().String(fmt.Sprintf(format, args...)))
	}
	sites, err := client.ListWebsites(ctx, hostinger.WebsiteFilter{Username: acc.ID})
	if err != nil {
		if hostinger.IsBlocking(err) {
			return nil, fmt.Errorf("%w: %v", ErrTokenRejected, err)
		}
		return nil, fmt.Errorf("gagal membaca daftar website akun %s: %w", acc.ID, err)
	}
	sort.Slice(sites, func(i, j int) bool { return sites[i].Domain < sites[j].Domain })
	var todo []hostinger.Website
	for _, site := range sites {
		if site.Domain != "" {
			todo = append(todo, site)
		}
	}
	s.report(Progress{AccountID: acc.ID, Stage: StageAccount, Total: len(todo)})
	s.loadAliasData(ctx, client, cache)
	if cache.portfolioErr != nil {
		warn("Portfolio domain tidak terbaca (%v); status domain ditentukan dari DNS.", cache.portfolioErr)
	}
	if cache.mailErr != nil {
		warn("Order email tidak terbaca (%v); mailbox tidak diinventaris.", cache.mailErr)
	}
	apiDBs, err := client.ListDatabases(ctx, acc.ID)
	if err != nil {
		warn("Daftar database tidak terbaca: %v", err)
	}
	if crons, err := client.ListCronJobs(ctx, acc.ID); err != nil {
		warn("Cron job tidak terbaca: %v", err)
	} else {
		for _, c := range crons {
			snap.Cron = append(snap.Cron, store.CronRow{UID: c.UID, Schedule: c.Time, Command: s.redactor().String(c.Command)})
		}
	}

	var ssh *sshx.Client
	if s.SSH != nil {
		info, _ := s.Store.GetSSH(ctx, acc.ID)
		if info != nil && info.Status == store.SSHReady {
			ssh, err = s.SSH.Connect(ctx, acc.ID)
			if err != nil {
				_, msg := sshx.Classify(err)
				if errors.Is(err, sshx.ErrBlocked) {
					msg = err.Error()
				}
				warn("SSH tidak bisa dipakai (%s); ukuran dan deteksi tipe terbatas.", msg)
				ssh = nil
			}
		} else {
			status := store.SSHNotConfigured
			if info != nil {
				status = info.Status
			}
			warn("SSH akun %s berstatus %s; ukuran tidak diketahui dan deteksi tipe memakai listing API (hanya public_html).", acc.ID, status)
		}
	}
	if ssh != nil {
		defer ssh.Close()
		snap.SSHUsed = true
	}

	dbByName := map[string]*store.DatabaseRow{}
	for _, d := range apiDBs {
		row := &store.DatabaseRow{AccountID: acc.ID, Name: d.Name, User: d.User, Host: d.Host, Port: d.Port, SizeBytes: d.DiskUsageMB * 1024 * 1024, InAPI: true, APIDomain: d.Domain, Source: "api"}
		dbByName[d.Name] = row
	}

	results := s.scanSites(ctx, client, ssh, cache, acc, todo)
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	for _, r := range results {
		snap.Warnings = append(snap.Warnings, r.warnings...)
		w := r.website
		for _, ref := range r.refs {
			row, ok := dbByName[ref.Name]
			if !ok {
				row = &store.DatabaseRow{AccountID: acc.ID, Name: ref.Name, Host: ref.Host}
				dbByName[ref.Name] = row
				w.Notes = append(w.Notes, fmt.Sprintf("Database %s dirujuk %s tetapi tidak ada di daftar database API akun ini.", ref.Name, ref.Source))
			}
			row.WebsiteDomain = w.Domain
			row.Source = ref.Source
			if row.User == "" {
				row.User = ref.User
			}
			if ref.Host != "" && ref.Host != "localhost" && ref.Host != "127.0.0.1" {
				row.Host = ref.Host
			}
		}
		snap.Websites = append(snap.Websites, w)
		snap.Mailboxes = append(snap.Mailboxes, r.mailboxes...)
	}
	markIncluded(snap.Websites)
	for _, d := range dbByName {
		if d.WebsiteDomain == "" && d.APIDomain != "" {
			d.WebsiteDomain = d.APIDomain
		}
		snap.Databases = append(snap.Databases, *d)
	}
	sort.Slice(snap.Databases, func(i, j int) bool { return snap.Databases[i].Name < snap.Databases[j].Name })

	if s.DataDir != "" {
		p, err := s.writeJSON(snap)
		if err != nil {
			warn("File JSON inventaris gagal ditulis: %v", err)
		} else {
			snap.JSONPath = p
		}
	}
	if err := s.Store.SaveInventory(ctx, snap); err != nil {
		return nil, err
	}
	if s.Log != nil {
		s.Log.Info("inventaris selesai", "alias", alias, "akun", acc.ID, "website", len(snap.Websites), "database", len(snap.Databases), "mailbox", len(snap.Mailboxes), "peringatan", len(snap.Warnings))
	}
	s.Store.AppendAudit(ctx, nil, "operator", "inventory.scan", acc.ID, "OK", fmt.Sprintf("alias %s: %d website, %d database, %d mailbox, %d peringatan", alias, len(snap.Websites), len(snap.Databases), len(snap.Mailboxes), len(snap.Warnings)))
	return snap, nil
}

func (s *Scanner) report(p Progress) {
	if s.Progress != nil {
		s.Progress(p)
	}
}

func (s *Scanner) scanSites(ctx context.Context, client *hostinger.Client, ssh *sshx.Client, cache *aliasCache, acc store.HostingAccount, sites []hostinger.Website) []siteResult {
	results := make([]siteResult, len(sites))
	workers := s.Parallel
	if workers <= 0 {
		workers = DefaultParallel
	}
	if workers > len(sites) {
		workers = len(sites)
	}
	var mu sync.Mutex
	done := 0
	jobs := make(chan int)
	var wg sync.WaitGroup
	for n := 0; n < workers; n++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				started := time.Now()
				r := s.scanSite(ctx, client, ssh, cache, acc, sites[i])
				results[i] = r
				mu.Lock()
				done++
				s.report(Progress{AccountID: acc.ID, Stage: StageWebsite, Done: done, Total: len(sites), Domain: r.website.Domain, Type: r.website.SiteType, SizeBytes: r.website.SizeBytes, Elapsed: time.Since(started), Warnings: len(r.warnings)})
				mu.Unlock()
			}
		}()
	}
	for i := range sites {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func (s *Scanner) scanSite(ctx context.Context, client *hostinger.Client, ssh *sshx.Client, cache *aliasCache, acc store.HostingAccount, site hostinger.Website) siteResult {
	var r siteResult
	warn := func(format string, args ...any) {
		r.warnings = append(r.warnings, s.redactor().String(fmt.Sprintf(format, args...)))
	}
	r.website, r.refs = s.scanWebsite(ctx, client, ssh, cache, acc, site, warn)
	if order, ok := cache.mailOrders[strings.ToLower(site.Domain)]; ok && site.VhostType != "parked" {
		boxes, err := client.ListMailboxes(ctx, order.ID)
		if err != nil {
			warn("Mailbox %s tidak terbaca: %v", site.Domain, err)
			return r
		}
		for _, b := range boxes {
			r.mailboxes = append(r.mailboxes, store.MailboxRow{
				Domain: site.Domain, Address: b.Address, APIID: b.ID, OrderID: order.ID, Status: b.Status, IsCatchall: b.IsCatchall,
				QuotaKB: b.Usage.StorageQuota, UsedKB: b.Usage.StorageUsed, MessageCount: b.Usage.MessagesUsed,
			})
		}
	}
	return r
}

func unitPath(root string) string {
	root = strings.TrimRight(root, "/")
	if strings.HasSuffix(root, "/public_html") {
		return path.Dir(root)
	}
	return root
}

func (s *Scanner) scanWebsite(ctx context.Context, client *hostinger.Client, ssh *sshx.Client, cache *aliasCache, acc store.HostingAccount, site hostinger.Website, warn func(string, ...any)) (store.Website, []detector.DBRef) {
	w := store.Website{
		AccountID:    acc.ID,
		Domain:       site.Domain,
		DocRoot:      site.RootDirectory,
		VhostType:    site.VhostType,
		ParentDomain: site.ParentDomain,
		IsEnabled:    site.IsEnabled,
		APIOrderID:   site.OrderID,
		APIType:      site.WebsiteType,
		SizeBytes:    store.UnknownQuantity,
		FileCount:    store.UnknownQuantity,
		SizeSource:   store.SizeUnknown,
	}
	if site.VhostType != "parked" && site.RootDirectory != "" {
		w.UnitPath = unitPath(site.RootDirectory)
	}
	if site.VhostType == "parked" {
		w.IncludedIn = site.ParentDomain
		w.SiteType = "Parked domain"
	}
	if order, ok := cache.mailOrders[strings.ToLower(site.Domain)]; ok {
		w.MailOrderID = order.ID
	}
	s.domainStatus(ctx, cache, &w)
	if zone, err := client.GetZone(ctx, site.Domain); err == nil {
		w.DNSInToken = true
		w.DNSRecordCount = len(zone)
	}
	if site.VhostType == "main" || site.VhostType == "addon" {
		if subs, err := client.ListSubdomains(ctx, acc.ID, site.Domain); err == nil {
			for _, sd := range subs {
				w.Subdomains = append(w.Subdomains, sd.Domain)
			}
		} else {
			warn("Subdomain %s tidak terbaca: %v", site.Domain, err)
		}
		if parked, err := client.ListParkedDomains(ctx, acc.ID, site.Domain); err == nil {
			for _, p := range parked {
				w.Parked = append(w.Parked, p.Domain)
			}
		} else {
			warn("Parked domain %s tidak terbaca: %v", site.Domain, err)
		}
	}
	if site.VhostType != "parked" {
		if php, err := client.GetPHPDetails(ctx, acc.ID, site.Domain); err == nil {
			w.PHPVersion = php.Version
		}
	}
	if site.VhostType == "parked" {
		return w, nil
	}
	in := detector.Input{APIType: site.WebsiteType, Files: map[string]string{}}
	if ssh != nil && w.UnitPath != "" {
		probe, err := s.probe(ctx, ssh, w.UnitPath)
		if s.SkipSize && err == nil && !probe.missing {
			w.Notes = append(w.Notes, "Ukuran dan jumlah file tidak dihitung (scan cepat).")
		}
		switch {
		case err != nil:
			warn("Probe SSH %s gagal: %v", site.Domain, err)
		case probe.missing:
			warn("Folder %s tidak ditemukan di server.", w.UnitPath)
		default:
			if probe.size >= 0 {
				w.SizeBytes = probe.size
				w.FileCount = probe.files
				w.SizeSource = store.SizeFromSSH
			}
			in.RootEntries = probe.root
			in.PublicEntries = probe.public
			in.Files = probe.content
		}
	} else if entries, err := client.ListFiles(ctx, acc.ID, site.Domain); err == nil {
		for _, e := range entries {
			name := e.Name
			if e.Type == "directory" {
				name += "/"
			}
			in.PublicEntries = append(in.PublicEntries, name)
		}
		w.Notes = append(w.Notes, "Deteksi dari listing API public_html (tanpa SSH); isi file konfigurasi tidak dibaca.")
	}
	for _, content := range in.Files {
		for _, secret := range detector.ExtractSecrets(content) {
			s.redactor().Add(secret)
		}
	}
	res := detector.Detect(in)
	w.SiteType = res.Type
	w.TypeConfidence = res.Confidence
	w.Signals = res.Signals
	w.Notes = append(w.Notes, res.Notes...)
	return w, res.DBRefs
}

var hostingerNS = []string{"dns-parking.com", "hostinger."}

func (s *Scanner) domainStatus(ctx context.Context, cache *aliasCache, w *store.Website) {
	if !w.IsEnabled {
		w.DomainStatus = store.DomainDisabled
	}
	if d, ok := cache.portfolio[strings.ToLower(w.Domain)]; ok {
		w.DomainExpiresAt = d.ExpiresAt
		switch d.Status {
		case "expired", "deleted", "suspended", "failed":
			w.DomainStatus = store.DomainExpired
		case "active":
			if w.DomainStatus == "" {
				w.DomainStatus = store.DomainActive
			}
		}
	}
	lookup := s.LookupNS
	if lookup == nil {
		lookup = defaultLookupNS
	}
	lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	ns, err := lookup(lctx, registrable(w.Domain))
	if err == nil && len(ns) > 0 {
		w.Nameservers = ns
		ext := true
		for _, n := range ns {
			for _, h := range hostingerNS {
				if strings.Contains(strings.ToLower(n), h) {
					ext = false
				}
			}
		}
		w.NSExternal = &ext
		if w.DomainStatus == "" {
			w.DomainStatus = store.DomainActive
		}
		return
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) && dnsErr.IsNotFound {
		if w.DomainStatus == "" || w.DomainStatus == store.DomainActive {
			w.DomainStatus = store.DomainExpired
		}
		w.Notes = append(w.Notes, "Domain tidak terdaftar di DNS publik (NXDOMAIN); kemungkinan expired.")
		return
	}
	if w.DomainStatus == "" {
		w.DomainStatus = store.DomainUnknown
	}
}

func registrable(domain string) string {
	parts := strings.Split(strings.ToLower(domain), ".")
	if len(parts) <= 2 {
		return strings.Join(parts, ".")
	}
	secondLevel := map[string]bool{"co": true, "com": true, "net": true, "org": true, "ac": true, "or": true, "go": true, "sch": true, "my": true, "web": true, "biz": true, "gov": true, "edu": true, "mil": true, "ponpes": true, "desa": true}
	if len(parts[len(parts)-1]) == 2 && secondLevel[parts[len(parts)-2]] {
		return strings.Join(parts[len(parts)-3:], ".")
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func defaultLookupNS(ctx context.Context, domain string) ([]string, error) {
	recs, err := net.DefaultResolver.LookupNS(ctx, domain)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(recs))
	for _, r := range recs {
		out = append(out, strings.TrimSuffix(r.Host, "."))
	}
	sort.Strings(out)
	return out, nil
}

func markIncluded(sites []store.Website) {
	for i := range sites {
		if sites[i].UnitPath == "" || sites[i].IncludedIn != "" {
			continue
		}
		for j := range sites {
			if i == j || sites[j].UnitPath == "" || sites[j].VhostType == "parked" {
				continue
			}
			if strings.HasPrefix(sites[i].UnitPath, sites[j].UnitPath+"/") {
				sites[i].IncludedIn = sites[j].Domain
				sites[i].Notes = append(sites[i].Notes, fmt.Sprintf("Folder berada di dalam folder %s; ikut dibackup bersama website tersebut.", sites[j].Domain))
				break
			}
		}
	}
}

var probeFiles = []string{
	"wp-config.php", "public_html/wp-config.php",
	".env", "public_html/.env",
	"composer.json", "public_html/composer.json",
	"package.json", "public_html/package.json",
	"configuration.php", "public_html/configuration.php",
	"application/config/database.php", "public_html/application/config/database.php",
	"app/Config/Database.php",
}

func ProbeCommand(unit string) string {
	return probeCommand(unit, true)
}

func QuickProbeCommand(unit string) string {
	return probeCommand(unit, false)
}

func probeCommand(unit string, withSize bool) string {
	var b strings.Builder
	b.WriteString("cd " + sshx.ShellQuote(unit) + " 2>/dev/null || { echo 'ERR=tidak_ada'; exit 0; }; ")
	if withSize {
		b.WriteString(`if find . -maxdepth 0 -printf '' >/dev/null 2>&1; then find . -type f -printf '%s\n' 2>/dev/null | awk '{s+=$1} END {printf "SIZE=%.0f\nFILES=%d\n", s, NR}'; `)
		b.WriteString(`else printf 'SIZE=%s\n' "$(du -sb . 2>/dev/null | cut -f1)"; printf 'FILES=%s\n' "$(find . -type f 2>/dev/null | wc -l)"; fi; `)
	}
	b.WriteString(`ls -1Ap . 2>/dev/null | sed 's|^|ROOT |'; `)
	b.WriteString(`if [ -d public_html ]; then ls -1Ap public_html 2>/dev/null | sed 's|^|PUB |'; fi; `)
	b.WriteString("for f in " + strings.Join(probeFiles, " ") + `; do if [ -f "$f" ]; then printf 'FILE %s\n' "$f"; head -c 65536 "$f" | base64 | tr -d '\n'; printf '\n'; fi; done`)
	return b.String()
}

type probeResult struct {
	missing bool
	size    int64
	files   int64
	root    []string
	public  []string
	content map[string]string
}

func ParseProbe(out string) (probeResult, error) {
	r := probeResult{size: -1, files: -1, content: map[string]string{}}
	lines := strings.Split(out, "\n")
	for i := 0; i < len(lines); i++ {
		line := strings.TrimRight(lines[i], "\r")
		switch {
		case line == "ERR=tidak_ada":
			r.missing = true
		case strings.HasPrefix(line, "SIZE="):
			if v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "SIZE=")), 10, 64); err == nil {
				r.size = v
			}
		case strings.HasPrefix(line, "FILES="):
			if v, err := strconv.ParseInt(strings.TrimSpace(strings.TrimPrefix(line, "FILES=")), 10, 64); err == nil {
				r.files = v
			}
		case strings.HasPrefix(line, "ROOT "):
			r.root = append(r.root, strings.TrimPrefix(line, "ROOT "))
		case strings.HasPrefix(line, "PUB "):
			r.public = append(r.public, strings.TrimPrefix(line, "PUB "))
		case strings.HasPrefix(line, "FILE "):
			name := strings.TrimPrefix(line, "FILE ")
			if i+1 < len(lines) {
				i++
				data, err := base64.StdEncoding.DecodeString(strings.TrimSpace(lines[i]))
				if err != nil {
					return r, fmt.Errorf("isi %s tidak bisa didekode", name)
				}
				r.content[name] = string(data)
			}
		}
	}
	return r, nil
}

func (s *Scanner) probe(ctx context.Context, c *sshx.Client, unit string) (probeResult, error) {
	wait := s.ProbeTimeout
	if wait <= 0 {
		wait = 10 * time.Minute
	}
	pctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	cmd := ProbeCommand(unit)
	if s.SkipSize {
		cmd = QuickProbeCommand(unit)
	}
	res, err := c.Run(pctx, cmd, nil)
	if err != nil {
		return probeResult{}, err
	}
	if res.ExitCode != 0 {
		return probeResult{}, fmt.Errorf("probe keluar dengan kode %d", res.ExitCode)
	}
	return ParseProbe(res.Stdout)
}

type jsonWebsite struct {
	Domain         string   `json:"domain"`
	UnitPath       string   `json:"folder_backup"`
	DocRoot        string   `json:"document_root"`
	Vhost          string   `json:"jenis_vhost"`
	IncludedIn     string   `json:"bagian_dari,omitempty"`
	Type           string   `json:"tipe"`
	Confidence     float64  `json:"keyakinan"`
	SizeBytes      int64    `json:"ukuran_byte"`
	FileCount      int64    `json:"jumlah_file"`
	DomainStatus   string   `json:"status_domain"`
	ExpiresAt      string   `json:"domain_kedaluwarsa,omitempty"`
	Enabled        bool     `json:"aktif_di_hpanel"`
	PHP            string   `json:"versi_php,omitempty"`
	NSExternal     *bool    `json:"ns_eksternal,omitempty"`
	Nameservers    []string `json:"nameserver,omitempty"`
	DNSInToken     bool     `json:"zone_dns_di_token"`
	DNSRecordCount int      `json:"jumlah_record_dns"`
	Subdomains     []string `json:"subdomain,omitempty"`
	Parked         []string `json:"parked_domain,omitempty"`
	Signals        []string `json:"sinyal_deteksi,omitempty"`
	Notes          []string `json:"catatan,omitempty"`
}

type jsonDB struct {
	Name      string `json:"nama"`
	User      string `json:"user"`
	Host      string `json:"host,omitempty"`
	Website   string `json:"website,omitempty"`
	Source    string `json:"sumber"`
	SizeBytes int64  `json:"ukuran_byte"`
	InAPI     bool   `json:"ada_di_api"`
}

type jsonMailbox struct {
	Address  string `json:"alamat"`
	Domain   string `json:"domain"`
	UsedKB   int64  `json:"terpakai_kb"`
	QuotaKB  int64  `json:"kuota_kb"`
	Messages int64  `json:"jumlah_pesan"`
}

type jsonInventory struct {
	Alias     string        `json:"alias"`
	Account   string        `json:"akun"`
	Generated string        `json:"dibuat"`
	SSHUsed   bool          `json:"memakai_ssh"`
	Websites  []jsonWebsite `json:"website"`
	Databases []jsonDB      `json:"database"`
	Mailboxes []jsonMailbox `json:"mailbox"`
	Cron      []jsonCron    `json:"cron"`
	Warnings  []string      `json:"peringatan"`
}

type jsonCron struct {
	UID      string `json:"uid"`
	Schedule string `json:"jadwal"`
	Command  string `json:"perintah"`
}

func (s *Scanner) writeJSON(snap *store.InventorySnapshot) (string, error) {
	dir := filepath.Join(s.DataDir, "inventory")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	doc := jsonInventory{Alias: snap.Alias, Account: snap.AccountID, Generated: time.Now().UTC().Format(time.RFC3339), SSHUsed: snap.SSHUsed, Warnings: snap.Warnings}
	for _, c := range snap.Cron {
		doc.Cron = append(doc.Cron, jsonCron{UID: c.UID, Schedule: c.Schedule, Command: c.Command})
	}
	for _, w := range snap.Websites {
		doc.Websites = append(doc.Websites, jsonWebsite{
			Domain: w.Domain, UnitPath: w.UnitPath, DocRoot: w.DocRoot, Vhost: w.VhostType, IncludedIn: w.IncludedIn, Type: w.SiteType,
			Confidence: w.TypeConfidence, SizeBytes: w.SizeBytes, FileCount: w.FileCount, DomainStatus: w.DomainStatus, ExpiresAt: w.DomainExpiresAt,
			Enabled: w.IsEnabled, PHP: w.PHPVersion, NSExternal: w.NSExternal, Nameservers: w.Nameservers, DNSInToken: w.DNSInToken,
			DNSRecordCount: w.DNSRecordCount, Subdomains: w.Subdomains, Parked: w.Parked, Signals: w.Signals, Notes: w.Notes,
		})
	}
	for _, d := range snap.Databases {
		doc.Databases = append(doc.Databases, jsonDB{Name: d.Name, User: d.User, Host: d.Host, Website: d.WebsiteDomain, Source: d.Source, SizeBytes: d.SizeBytes, InAPI: d.InAPI})
	}
	for _, m := range snap.Mailboxes {
		doc.Mailboxes = append(doc.Mailboxes, jsonMailbox{Address: m.Address, Domain: m.Domain, UsedKB: m.UsedKB, QuotaKB: m.QuotaKB, Messages: m.MessageCount})
	}
	data, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return "", err
	}
	if data, err = redactJSONStrings(data, s.redactor()); err != nil {
		return "", err
	}
	p := filepath.Join(dir, fmt.Sprintf("%s-%s.json", snap.Alias, snap.AccountID))
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return "", err
	}
	return p, os.Rename(tmp, p)
}

func redactJSONStrings(data []byte, r *logx.Redactor) ([]byte, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, err
	}
	return json.MarshalIndent(redactValue(v, r), "", "  ")
}

func redactValue(v any, r *logx.Redactor) any {
	switch x := v.(type) {
	case string:
		return r.String(x)
	case []any:
		for i := range x {
			x[i] = redactValue(x[i], r)
		}
	case map[string]any:
		for k, e := range x {
			x[k] = redactValue(e, r)
		}
	}
	return v
}
