package verify

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"regexp"
	"strings"
	"time"

	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

const (
	Pass = "PASS"
	Warn = "WARN"
	Fail = "FAIL"
)

type Check struct {
	Name   string
	Result string
	Detail string
}

type Report struct {
	Method string
	Checks []Check
}

func (r *Report) add(name, result, detail string) {
	r.Checks = append(r.Checks, Check{Name: name, Result: result, Detail: detail})
}

func (r *Report) Overall() string {
	out := Pass
	for _, c := range r.Checks {
		if c.Result == Fail {
			return Fail
		}
		if c.Result == Warn {
			out = Warn
		}
	}
	return out
}

func (r *Report) Verifications(prefix string) []store.Verification {
	var out []store.Verification
	for _, c := range r.Checks {
		out = append(out, store.Verification{CheckType: prefix + c.Name, Result: c.Result, Detail: c.Detail})
	}
	return out
}

type DBCheck struct {
	Name     string
	User     string
	Password string
	Host     string
}

type Target struct {
	Domain      string
	DummyDomain string
	SiteType    string
	DummyOnly   bool
	DestUnit    string
	MarkerPath  string
	FileCount   int64
	DBs         []DBCheck
}

type Verifier struct {
	Resolve      func(ctx context.Context, host string) (string, error)
	DialOverride func(addr string) string
	DummyScheme  string
	HostScheme   string
	SourceTitle  func(ctx context.Context, domain string) (string, error)
	Timeout      time.Duration
	MinAssets    int
}

func (v *Verifier) timeout() time.Duration {
	if v.Timeout > 0 {
		return v.Timeout
	}
	return 30 * time.Second
}

func (v *Verifier) client(targetIP string) *http.Client {
	dialer := &net.Dialer{Timeout: v.timeout()}
	tr := &http.Transport{
		DialContext: func(ctx context.Context, network, addr string) (net.Conn, error) {
			if targetIP != "" {
				_, port, _ := net.SplitHostPort(addr)
				addr = net.JoinHostPort(targetIP, port)
			}
			if v.DialOverride != nil {
				addr = v.DialOverride(addr)
			}
			return dialer.DialContext(ctx, network, addr)
		},
		TLSClientConfig: &tls.Config{},
	}
	return &http.Client{Transport: tr, Timeout: v.timeout(), CheckRedirect: func(req *http.Request, via []*http.Request) error { return http.ErrUseLastResponse }}
}

func (v *Verifier) resolve(ctx context.Context, host string) (string, error) {
	if v.Resolve != nil {
		return v.Resolve(ctx, host)
	}
	ips, err := net.DefaultResolver.LookupHost(ctx, host)
	if err != nil {
		return "", err
	}
	for _, ip := range ips {
		if strings.Count(ip, ".") == 3 {
			return ip, nil
		}
	}
	if len(ips) > 0 {
		return ips[0], nil
	}
	return "", fmt.Errorf("tidak ada IP untuk %s", host)
}

var errorSignatures = []string{
	"Error establishing a database connection",
	"Whoops, looks like something went wrong",
	"Whoops!",
	"Fatal error",
	"Parse error",
	"500 Internal Server Error",
	"Index of /",
	"SQLSTATE[",
}

var titleRe = regexp.MustCompile(`(?is)<title[^>]*>(.*?)</title>`)
var assetRe = regexp.MustCompile(`(?i)<(?:link[^>]+href|script[^>]+src|img[^>]+src)\s*=\s*["']([^"'#]+)["']`)

func Title(body string) string {
	m := titleRe.FindStringSubmatch(body)
	if m == nil {
		return ""
	}
	return strings.Join(strings.Fields(m[1]), " ")
}

func UsesHostHeader(siteType string) bool {
	return siteType != "Statis" && siteType != "Node.js"
}

type page struct {
	status   int
	location string
	body     string
}

func (v *Verifier) get(ctx context.Context, c *http.Client, rawURL, host string) (*page, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if host != "" {
		req.Host = host
	}
	req.Header.Set("User-Agent", "hostinger-migrator-verifikasi")
	res, err := c.Do(req)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return &page{status: res.StatusCode, location: res.Header.Get("Location"), body: string(data)}, nil
}

func (v *Verifier) HTTP(ctx context.Context, t Target, rep *Report) (string, error) {
	dummyScheme := v.DummyScheme
	if dummyScheme == "" {
		dummyScheme = "https"
	}
	hostScheme := v.HostScheme
	if hostScheme == "" {
		hostScheme = "http"
	}
	var c *http.Client
	var base, host, ip string
	var p *page
	var err error
	if UsesHostHeader(t.SiteType) {
		ip, err = v.resolve(ctx, t.DummyDomain)
		if err != nil {
			rep.add("HTTP", Fail, "IP server tujuan tidak bisa ditentukan dari "+t.DummyDomain+": "+err.Error())
			return "", nil
		}
		c = v.client(ip)
		base, host = hostScheme+"://"+t.Domain, t.Domain
		rep.Method = fmt.Sprintf("header Host: %s ke IP %s", t.Domain, ip)
		p, err = v.get(ctx, c, base+"/", host)
		if err != nil || !siteLike(p, t) {
			reason := "tidak merespons"
			if err == nil {
				reason = fmt.Sprintf("HTTP %d", p.status)
			}
			c = v.client("")
			base, host = dummyScheme+"://"+t.DummyDomain, ""
			rep.Method = fmt.Sprintf("header Host ke IP %s %s (domain belum terpasang di server tujuan), fallback GET langsung ke %s", ip, reason, base)
			p, err = v.get(ctx, c, base+"/", "")
		}
	} else {
		c = v.client("")
		base = dummyScheme + "://" + t.DummyDomain
		rep.Method = "GET langsung ke " + base
		p, err = v.get(ctx, c, base+"/", "")
		if ip2, rerr := v.resolve(ctx, t.DummyDomain); rerr == nil {
			ip = ip2
		}
	}
	if err != nil {
		rep.add("HTTP", Fail, "Request gagal: "+err.Error())
		return ip, nil
	}
	switch {
	case p.status == 200:
		rep.add("HTTP", Pass, fmt.Sprintf("HTTP 200 dari %s", base))
	case p.status >= 300 && p.status < 400:
		loc, _ := url.Parse(p.location)
		if loc != nil && loc.Host != "" && !sameSite(loc.Hostname(), t) {
			rep.add("HTTP", Fail, fmt.Sprintf("Redirect %d ke domain lain: %s", p.status, p.location))
			return ip, nil
		}
		rep.add("HTTP", Warn, fmt.Sprintf("Redirect %d ke %s; isi halaman tidak bisa dicek lewat metode ini", p.status, p.location))
		return ip, nil
	default:
		rep.add("HTTP", Fail, fmt.Sprintf("HTTP %d dari %s", p.status, base))
		return ip, nil
	}
	for _, sig := range errorSignatures {
		if strings.Contains(p.body, sig) {
			rep.add("TANDA_ERROR", Fail, "Halaman memuat tanda error: "+sig)
			return ip, nil
		}
	}
	rep.add("TANDA_ERROR", Pass, "Tidak ada tanda error di halaman utama")
	if t.DummyOnly {
		return ip, nil
	}
	title := Title(p.body)
	if v.SourceTitle != nil {
		if src, err := v.SourceTitle(ctx, t.Domain); err == nil && src != "" {
			if src == title {
				rep.add("JUDUL", Pass, "Judul sama dengan situs sumber: "+title)
			} else {
				rep.add("JUDUL", Warn, fmt.Sprintf("Judul berbeda: sumber %q, tujuan %q", src, title))
			}
		} else {
			rep.add("JUDUL", Warn, "Situs sumber tidak bisa diakses untuk membandingkan judul (judul tujuan: "+title+")")
		}
	}
	v.assets(ctx, c, base, host, p.body, t, rep)
	return ip, nil
}

func sameSite(h string, t Target) bool {
	return strings.EqualFold(h, t.Domain) || strings.EqualFold(h, "www."+t.Domain) || strings.EqualFold(h, t.DummyDomain)
}

func siteLike(p *page, t Target) bool {
	if p == nil {
		return false
	}
	if p.status == 200 {
		return true
	}
	if p.status >= 300 && p.status < 400 {
		loc, err := url.Parse(p.location)
		return err == nil && (loc.Host == "" || sameSite(loc.Hostname(), t))
	}
	return false
}

func (v *Verifier) assets(ctx context.Context, c *http.Client, base, host, body string, t Target, rep *Report) {
	min := v.MinAssets
	if min <= 0 {
		min = 3
	}
	baseURL, _ := url.Parse(base + "/")
	var urls []string
	seen := map[string]bool{}
	for _, m := range assetRe.FindAllStringSubmatch(body, -1) {
		u, err := baseURL.Parse(strings.TrimSpace(m[1]))
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
			continue
		}
		h := u.Hostname()
		if !strings.EqualFold(h, t.Domain) && !strings.EqualFold(h, "www."+t.Domain) && !strings.EqualFold(h, t.DummyDomain) {
			continue
		}
		u.Scheme = baseURL.Scheme
		u.Host = baseURL.Host
		if !seen[u.String()] {
			seen[u.String()] = true
			urls = append(urls, u.String())
		}
	}
	if len(urls) == 0 {
		rep.add("ASSET", Warn, "Tidak ada asset lokal (CSS/JS/gambar) di halaman utama")
		return
	}
	ok := 0
	var bad []string
	for i, u := range urls {
		if i >= min*3 || ok >= min {
			break
		}
		p, err := v.get(ctx, c, u, host)
		if err == nil && p.status == 200 {
			ok++
		} else if err == nil {
			bad = append(bad, fmt.Sprintf("%s (%d)", path.Base(u), p.status))
		} else {
			bad = append(bad, path.Base(u))
		}
	}
	switch {
	case ok >= min:
		rep.add("ASSET", Pass, fmt.Sprintf("%d asset berstatus 200", ok))
	case ok > 0 && len(urls) < min:
		rep.add("ASSET", Warn, fmt.Sprintf("Hanya %d asset lokal di halaman, semuanya 200", ok))
	case ok == 0:
		rep.add("ASSET", Fail, "Tidak ada asset yang berstatus 200: "+strings.Join(bad, ", "))
	default:
		rep.add("ASSET", Warn, fmt.Sprintf("%d asset 200, gagal: %s", ok, strings.Join(bad, ", ")))
	}
}

func (v *Verifier) Server(ctx context.Context, c *sshx.Client, t Target, rep *Report) {
	q := sshx.ShellQuote
	if t.FileCount > 0 {
		res, err := c.Run(ctx, "find "+q(t.DestUnit)+" -type f | wc -l", nil)
		if err != nil || res.ExitCode != 0 {
			rep.add("FILE", Fail, "Jumlah file tujuan tidak bisa dihitung")
		} else {
			var n int64
			fmt.Sscan(strings.TrimSpace(res.Stdout), &n)
			if n >= t.FileCount {
				rep.add("FILE", Pass, fmt.Sprintf("%d file di tujuan (arsip %d)", n, t.FileCount))
			} else {
				rep.add("FILE", Fail, fmt.Sprintf("Hanya %d file di tujuan, arsip %d", n, t.FileCount))
			}
		}
	}
	for _, db := range t.DBs {
		res, err := c.Run(ctx, "mysql -N -B -h "+q(db.Host)+" -u "+q(db.User)+" -e 'SELECT 1' "+q(db.Name), map[string]string{"MYSQL_PWD": db.Password})
		if err == nil && res.ExitCode == 0 && strings.TrimSpace(res.Stdout) == "1" {
			rep.add("DB_"+db.Name, Pass, "Query SELECT 1 ke "+db.Name+" berhasil")
		} else {
			detail := "Query ke " + db.Name + " gagal"
			if res != nil {
				detail += ": " + strings.TrimSpace(res.Stderr)
			}
			rep.add("DB_"+db.Name, Fail, detail)
		}
	}
	if t.MarkerPath != "" {
		res, err := c.Run(ctx, "find "+q(t.DestUnit)+" -name error_log -type f -newer "+q(t.MarkerPath)+" 2>/dev/null | head -5", nil)
		if err == nil && res.ExitCode == 0 {
			if files := strings.TrimSpace(res.Stdout); files != "" {
				rep.add("ERROR_LOG", Warn, "error_log baru ditemukan: "+strings.ReplaceAll(files, "\n", ", "))
			} else {
				rep.add("ERROR_LOG", Pass, "Tidak ada error_log baru")
			}
		}
	}
}

func FetchTitle(ctx context.Context, domain string, timeout time.Duration) (string, error) {
	c := &http.Client{Timeout: timeout}
	for _, scheme := range []string{"https", "http"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, scheme+"://"+domain+"/", nil)
		res, err := c.Do(req)
		if err != nil {
			continue
		}
		data, _ := io.ReadAll(io.LimitReader(res.Body, 2<<20))
		res.Body.Close()
		if res.StatusCode == 200 {
			return Title(string(data)), nil
		}
	}
	return "", fmt.Errorf("situs %s tidak bisa diakses", domain)
}

func SearchReplaceCommand(unit, from, to string) string {
	q := sshx.ShellQuote
	return "cd " + q(path.Join(unit, "public_html")) + " && wp search-replace " + q(from) + " " + q(to) + " --all-tables-with-prefix --skip-columns=guid --precise --quiet && wp search-replace " +
		q(strings.Replace(from, "https://", "http://", 1)) + " " + q(to) + " --all-tables-with-prefix --skip-columns=guid --precise --quiet"
}

func (v *Verifier) FinalCheck(ctx context.Context, domain, ip string, sslActive bool, httpsScheme string, rep *Report) {
	c := v.client(ip)
	if httpsScheme == "" {
		httpsScheme = "https"
	}
	check := func(name string, p *page) bool {
		for _, sig := range errorSignatures {
			if strings.Contains(p.body, sig) {
				rep.add(name, Fail, "Halaman memuat tanda error: "+sig)
				return false
			}
		}
		return true
	}
	p, err := v.get(ctx, c, "http://"+domain+"/", domain)
	switch {
	case err != nil:
		rep.add("HTTP", Fail, "http://"+domain+" tidak merespons: "+err.Error())
	case p.status == 200:
		if check("HTTP", p) {
			if sslActive {
				rep.add("HTTP", Warn, "http://"+domain+" HTTP 200 tanpa redirect ke HTTPS")
			} else {
				rep.add("HTTP", Pass, "http://"+domain+" HTTP 200")
			}
		}
	case p.status >= 300 && p.status < 400 && strings.HasPrefix(strings.ToLower(p.location), "https://"):
		rep.add("HTTP", Pass, fmt.Sprintf("http://%s redirect %d ke HTTPS", domain, p.status))
	default:
		rep.add("HTTP", Fail, fmt.Sprintf("http://%s HTTP %d", domain, p.status))
	}
	if !sslActive {
		rep.add("HTTPS", Warn, "SSL belum aktif; HTTPS belum bisa diverifikasi")
		return
	}
	p, err = v.get(ctx, c, httpsScheme+"://"+domain+"/", domain)
	switch {
	case err != nil:
		rep.add("HTTPS", Fail, "https://"+domain+" gagal: "+err.Error())
	case p.status == 200:
		if check("HTTPS", p) {
			rep.add("HTTPS", Pass, "https://"+domain+" HTTP 200")
		}
	default:
		rep.add("HTTPS", Fail, fmt.Sprintf("https://%s HTTP %d", domain, p.status))
	}
}
