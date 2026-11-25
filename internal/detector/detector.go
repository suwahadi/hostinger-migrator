package detector

import (
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

const (
	TypeWordPress   = "WordPress"
	TypeLaravel     = "Laravel"
	TypeCodeIgniter = "CodeIgniter"
	TypeJoomla      = "Joomla"
	TypeNodeJS      = "Node.js"
	TypePHP         = "PHP native"
	TypeStatic      = "Statis"
	TypeOther       = "Lainnya"
)

type Input struct {
	RootEntries   []string
	PublicEntries []string
	Files         map[string]string
	APIType       string
}

type DBRef struct {
	Name        string
	User        string
	Host        string
	Source      string
	HasPassword bool
	Password    string `json:"-"`
}

type Result struct {
	Type       string
	Confidence float64
	Signals    []string
	DBRefs     []DBRef
	Notes      []string
}

type listing struct {
	dirs  map[string]bool
	files map[string]bool
}

func newListing(entries []string) listing {
	l := listing{dirs: map[string]bool{}, files: map[string]bool{}}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" || e == "./" || e == "../" {
			continue
		}
		if strings.HasSuffix(e, "/") {
			l.dirs[strings.TrimSuffix(e, "/")] = true
		} else {
			l.files[e] = true
		}
	}
	return l
}

func (l listing) hasDir(n string) bool  { return l.dirs[n] }
func (l listing) hasFile(n string) bool { return l.files[n] }

func (l listing) hasExt(ext string) bool {
	for f := range l.files {
		if strings.HasSuffix(strings.ToLower(f), ext) {
			return true
		}
	}
	return false
}

func Detect(in Input) Result {
	root := newListing(in.RootEntries)
	pub := newListing(in.PublicEntries)
	file := func(names ...string) (string, string, bool) {
		for _, n := range names {
			if v, ok := in.Files[n]; ok {
				return n, v, true
			}
		}
		return "", "", false
	}
	var r Result
	addSignal := func(s string) { r.Signals = append(r.Signals, s) }

	wpLocs := []struct {
		l      listing
		prefix string
	}{{pub, "public_html/"}, {root, ""}}
	for _, loc := range wpLocs {
		if loc.l.hasFile("wp-config.php") && loc.l.hasDir("wp-content") && loc.l.hasDir("wp-includes") {
			r.Type, r.Confidence = TypeWordPress, 0.95
			addSignal(loc.prefix + "wp-config.php")
			addSignal(loc.prefix + "wp-content/")
			addSignal(loc.prefix + "wp-includes/")
			break
		}
		if r.Type == "" && loc.l.hasFile("wp-config.php") {
			r.Type, r.Confidence = TypeWordPress, 0.6
			addSignal(loc.prefix + "wp-config.php")
		}
	}
	if r.Type == TypeWordPress {
		if name, content, ok := file("public_html/wp-config.php", "wp-config.php"); ok {
			r.addRef(parseWPConfig(content), name)
		}
		r.finish()
		return r
	}

	if root.hasFile("artisan") || pub.hasFile("artisan") {
		prefix := ""
		if !root.hasFile("artisan") {
			prefix = "public_html/"
		}
		addSignal(prefix + "artisan")
		r.Type, r.Confidence = TypeLaravel, 0.6
		if _, content, ok := file(prefix+"composer.json", "composer.json", "public_html/composer.json"); ok && strings.Contains(content, "laravel/framework") {
			r.Confidence = 0.95
			addSignal(prefix + "composer.json: laravel/framework")
		}
		if prefix == "" {
			r.Notes = append(r.Notes, "Kode Laravel berada di luar public_html; seluruh folder domain dibackup.")
		}
		if name, content, ok := file(prefix+".env", ".env", "public_html/.env"); ok {
			r.addRef(parseLaravelEnv(content), name)
		}
		r.finish()
		return r
	}

	for _, loc := range wpLocs {
		if loc.l.hasDir("system") && loc.l.hasDir("application") {
			r.Type, r.Confidence = TypeCodeIgniter, 0.85
			addSignal(loc.prefix + "system/")
			addSignal(loc.prefix + "application/")
			if name, content, ok := file(loc.prefix+"application/config/database.php", "application/config/database.php", "public_html/application/config/database.php"); ok {
				r.addRef(parseCI3(content), name)
			}
			r.finish()
			return r
		}
		if loc.l.hasDir("app") && (loc.l.hasFile("spark") || composerHas(in.Files, loc.prefix+"composer.json", "codeigniter4/framework")) {
			r.Type, r.Confidence = TypeCodeIgniter, 0.9
			addSignal(loc.prefix + "app/")
			addSignal(loc.prefix + "spark / codeigniter4")
			if name, content, ok := file(loc.prefix+".env", ".env", "public_html/.env"); ok {
				r.addRef(parseCI4Env(content), name)
			}
			if len(r.DBRefs) == 0 {
				if name, content, ok := file(loc.prefix+"app/Config/Database.php", "app/Config/Database.php"); ok {
					r.addRef(parseCI3(content), name)
				}
			}
			r.finish()
			return r
		}
	}

	for _, loc := range wpLocs {
		if loc.l.hasFile("configuration.php") && loc.l.hasDir("administrator") {
			r.Type, r.Confidence = TypeJoomla, 0.9
			addSignal(loc.prefix + "configuration.php")
			addSignal(loc.prefix + "administrator/")
			if name, content, ok := file(loc.prefix+"configuration.php", "public_html/configuration.php"); ok {
				r.addRef(parseJoomla(content), name)
			}
			r.finish()
			return r
		}
	}

	hasStart := false
	pkgName := ""
	for _, n := range []string{"package.json", "public_html/package.json"} {
		if content, ok := in.Files[n]; ok && packageHasStart(content) {
			hasStart = true
			pkgName = n
			break
		}
	}
	apiNode := strings.EqualFold(in.APIType, "nodejs")
	if apiNode || hasStart {
		switch {
		case apiNode && hasStart:
			r.Type, r.Confidence = TypeNodeJS, 0.95
			addSignal("API website_type=nodejs")
			addSignal(pkgName + ": scripts.start")
		case apiNode:
			r.Type, r.Confidence = TypeNodeJS, 0.8
			addSignal("API website_type=nodejs")
		default:
			r.Notes = append(r.Notes, "package.json dengan scripts.start ditemukan, tetapi hPanel tidak menandai website ini sebagai Node.js; dianggap build frontend.")
		}
		if r.Type == TypeNodeJS {
			if name, content, ok := file(".env", "public_html/.env"); ok {
				r.addRef(parseLaravelEnv(content), name)
			}
			r.finish()
			return r
		}
	}

	if pub.hasExt(".php") || root.hasExt(".php") {
		r.Type, r.Confidence = TypePHP, 0.6
		addSignal("file .php di root website")
		r.Notes = append(r.Notes, "Database PHP native tidak bisa dideteksi otomatis; operator ditanya saat rencana bila website memakai database.")
		r.finish()
		return r
	}
	if pub.hasFile("index.html") || pub.hasFile("index.htm") || root.hasFile("index.html") {
		r.Type, r.Confidence = TypeStatic, 0.8
		addSignal("index.html tanpa file .php")
		r.finish()
		return r
	}
	r.Type, r.Confidence = TypeOther, 0.3
	if strings.EqualFold(in.APIType, "wordpress") {
		r.Notes = append(r.Notes, "API menandai website ini WordPress, tetapi file WordPress tidak terlihat di listing.")
	}
	r.Notes = append(r.Notes, "Tipe tidak dikenali; dibackup dan direstore apa adanya.")
	r.finish()
	return r
}

func (r *Result) addRef(ref DBRef, source string) {
	if ref.Name == "" {
		return
	}
	ref.Source = source
	if ref.Host == "" {
		ref.Host = "localhost"
	}
	r.DBRefs = append(r.DBRefs, ref)
}

func (r *Result) finish() {
	sort.Strings(r.Signals)
}

func composerHas(files map[string]string, name, pkg string) bool {
	return strings.Contains(files[name], pkg)
}

func packageHasStart(content string) bool {
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal([]byte(content), &pkg) != nil {
		return false
	}
	return strings.TrimSpace(pkg.Scripts["start"]) != ""
}

var wpDefine = regexp.MustCompile(`define\(\s*['"](DB_NAME|DB_USER|DB_PASSWORD|DB_HOST)['"]\s*,\s*(?:'([^']*)'|"([^"]*)")\s*\)`)

func parseWPConfig(content string) DBRef {
	var ref DBRef
	for _, m := range wpDefine.FindAllStringSubmatch(stripPHPComments(content), -1) {
		v := m[2] + m[3]
		switch m[1] {
		case "DB_NAME":
			ref.Name = v
		case "DB_USER":
			ref.User = v
		case "DB_HOST":
			ref.Host = v
		case "DB_PASSWORD":
			ref.HasPassword = v != ""
			ref.Password = v
		}
	}
	return ref
}

var (
	phpBlockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	phpLineComment  = regexp.MustCompile(`(?m)^[ \t]*(?://|#)[^\n]*`)
)

func stripPHPComments(s string) string {
	return phpLineComment.ReplaceAllString(phpBlockComment.ReplaceAllString(s, ""), "")
}

func ParseEnv(content string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.TrimSpace(v)
		if len(v) >= 2 && (v[0] == '"' && v[len(v)-1] == '"' || v[0] == '\'' && v[len(v)-1] == '\'') {
			v = v[1 : len(v)-1]
		} else if i := strings.Index(v, " #"); i >= 0 {
			v = strings.TrimSpace(v[:i])
		}
		out[strings.TrimSpace(k)] = v
	}
	return out
}

func parseLaravelEnv(content string) DBRef {
	env := ParseEnv(content)
	if c := env["DB_CONNECTION"]; c != "" && c != "mysql" && c != "mariadb" {
		return DBRef{}
	}
	return DBRef{Name: env["DB_DATABASE"], User: env["DB_USERNAME"], Host: env["DB_HOST"], HasPassword: env["DB_PASSWORD"] != "", Password: env["DB_PASSWORD"]}
}

func parseCI4Env(content string) DBRef {
	env := ParseEnv(content)
	return DBRef{
		Name:        env["database.default.database"],
		User:        env["database.default.username"],
		Host:        env["database.default.hostname"],
		HasPassword: env["database.default.password"] != "",
		Password:    env["database.default.password"],
	}
}

var ciField = regexp.MustCompile(`['"](hostname|username|password|database)['"]\s*=>\s*(?:'([^']*)'|"([^"]*)")`)

func parseCI3(content string) DBRef {
	var ref DBRef
	for _, m := range ciField.FindAllStringSubmatch(stripPHPComments(content), -1) {
		v := m[2] + m[3]
		switch m[1] {
		case "hostname":
			if ref.Host == "" {
				ref.Host = v
			}
		case "username":
			if ref.User == "" {
				ref.User = v
			}
		case "database":
			if ref.Name == "" {
				ref.Name = v
			}
		case "password":
			if v != "" && !ref.HasPassword {
				ref.HasPassword = true
				ref.Password = v
			}
		}
	}
	return ref
}

var joomlaField = regexp.MustCompile(`public\s+\$(host|user|password|db)\s*=\s*(?:'([^']*)'|"([^"]*)")`)

func parseJoomla(content string) DBRef {
	var ref DBRef
	for _, m := range joomlaField.FindAllStringSubmatch(content, -1) {
		v := m[2] + m[3]
		switch m[1] {
		case "host":
			ref.Host = v
		case "user":
			ref.User = v
		case "db":
			ref.Name = v
		case "password":
			ref.HasPassword = v != ""
			ref.Password = v
		}
	}
	return ref
}

var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`define\(\s*['"]DB_PASSWORD['"]\s*,\s*(?:'([^']*)'|"([^"]*)")`),
	regexp.MustCompile(`(?m)^\s*(?:DB_PASSWORD|database\.default\.password|APP_KEY|[A-Z0-9_]*(?:SECRET|TOKEN|PASSWORD|_KEY))\s*=\s*['"]?([^'"\r\n#]+)`),
	regexp.MustCompile(`['"]password['"]\s*=>\s*(?:'([^']*)'|"([^"]*)")`),
	regexp.MustCompile(`public\s+\$(?:password|secret)\s*=\s*(?:'([^']*)'|"([^"]*)")`),
}

var envReference = regexp.MustCompile(`^\$\{[A-Za-z_][A-Za-z0-9_]*\}$`)

func trivialValue(s string) bool {
	switch strings.ToLower(s) {
	case "true", "false", "null", "none":
		return true
	}
	return envReference.MatchString(s)
}

func ExtractSecrets(content string) []string {
	var out []string
	for _, re := range secretPatterns {
		for _, m := range re.FindAllStringSubmatch(content, -1) {
			for _, g := range m[1:] {
				g = strings.TrimSpace(g)
				if len(g) >= 4 && !trivialValue(g) {
					out = append(out, g)
				}
			}
		}
	}
	return out
}
