package detector

import (
	"fmt"
	"regexp"
	"strings"
)

type Credential struct {
	Name     string
	User     string
	Password string
	Host     string
}

func CredentialsFrom(source, content string) DBRef {
	switch {
	case strings.HasSuffix(source, "wp-config.php"):
		return parseWPConfig(content)
	case strings.HasSuffix(source, "configuration.php"):
		return parseJoomla(content)
	case strings.HasSuffix(source, "database.php"), strings.HasSuffix(source, "Database.php"):
		return parseCI3(content)
	case strings.HasSuffix(source, ".env"):
		if ref := parseLaravelEnv(content); ref.Name != "" {
			return ref
		}
		return parseCI4Env(content)
	}
	return DBRef{}
}

func phpQuote(v string) string {
	return strings.NewReplacer(`\`, `\\`, `'`, `\'`).Replace(v)
}

func envValue(v string) string {
	if v == "" || strings.ContainsAny(v, " #\"'$`\\=") {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(v) + `"`
	}
	return v
}

func replacePHPDefine(content, key, value string) (string, bool) {
	re := regexp.MustCompile(`(?m)^([ \t]*define\(\s*['"]` + key + `['"]\s*,\s*)(?:'[^']*'|"[^"]*")(\s*\))`)
	if !re.MatchString(content) {
		return content, false
	}
	return re.ReplaceAllStringFunc(content, func(m string) string {
		sub := re.FindStringSubmatch(m)
		return sub[1] + "'" + phpQuote(value) + "'" + sub[2]
	}), true
}

func replaceEnv(content, key, value string) (string, bool) {
	re := regexp.MustCompile(`(?m)^(\s*(?:export\s+)?` + regexp.QuoteMeta(key) + `\s*=\s*).*$`)
	if !re.MatchString(content) {
		return content, false
	}
	return re.ReplaceAllStringFunc(content, func(m string) string {
		sub := re.FindStringSubmatch(m)
		return sub[1] + envValue(value)
	}), true
}

func replaceFirst(content string, re *regexp.Regexp, build func(sub []string) string) (string, bool) {
	loc := re.FindStringSubmatchIndex(content)
	if loc == nil {
		return content, false
	}
	sub := make([]string, len(loc)/2)
	for i := range sub {
		if loc[2*i] >= 0 {
			sub[i] = content[loc[2*i]:loc[2*i+1]]
		}
	}
	return content[:loc[0]] + build(sub) + content[loc[1]:], true
}

func UpdateCredentials(source, content string, c Credential) (string, error) {
	var missing []string
	set := func(ok bool, key string) {
		if !ok {
			missing = append(missing, key)
		}
	}
	out := content
	var ok bool
	switch {
	case strings.HasSuffix(source, "wp-config.php"):
		for _, kv := range [][2]string{{"DB_NAME", c.Name}, {"DB_USER", c.User}, {"DB_PASSWORD", c.Password}, {"DB_HOST", c.Host}} {
			out, ok = replacePHPDefine(out, kv[0], kv[1])
			set(ok, kv[0])
		}
	case strings.HasSuffix(source, "configuration.php"):
		for _, kv := range [][2]string{{"db", c.Name}, {"user", c.User}, {"password", c.Password}, {"host", c.Host}} {
			re := regexp.MustCompile(`(public\s+\$` + kv[0] + `\s*=\s*)(?:'[^']*'|"[^"]*")`)
			val := kv[1]
			out, ok = replaceFirst(out, re, func(sub []string) string { return sub[1] + "'" + phpQuote(val) + "'" })
			set(ok, kv[0])
		}
	case strings.HasSuffix(source, "database.php"), strings.HasSuffix(source, "Database.php"):
		for _, kv := range [][2]string{{"database", c.Name}, {"username", c.User}, {"password", c.Password}, {"hostname", c.Host}} {
			re := regexp.MustCompile(`(['"]` + kv[0] + `['"]\s*=>\s*)(?:'[^']*'|"[^"]*")`)
			val := kv[1]
			out, ok = replaceFirst(out, re, func(sub []string) string { return sub[1] + "'" + phpQuote(val) + "'" })
			set(ok, kv[0])
		}
	case strings.HasSuffix(source, ".env"):
		keys := [][2]string{{"DB_DATABASE", c.Name}, {"DB_USERNAME", c.User}, {"DB_PASSWORD", c.Password}, {"DB_HOST", c.Host}}
		if _, has := ParseEnv(content)["database.default.database"]; has {
			keys = [][2]string{{"database.default.database", c.Name}, {"database.default.username", c.User}, {"database.default.password", c.Password}, {"database.default.hostname", c.Host}}
		}
		for _, kv := range keys {
			out, ok = replaceEnv(out, kv[0], kv[1])
			set(ok, kv[0])
		}
	default:
		return content, fmt.Errorf("format file konfigurasi %s tidak dikenali", source)
	}
	if len(missing) > 0 {
		return content, fmt.Errorf("kunci tidak ditemukan di %s: %s", source, strings.Join(missing, ", "))
	}
	return out, nil
}
