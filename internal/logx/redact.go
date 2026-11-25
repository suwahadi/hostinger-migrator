package logx

import (
	"regexp"
	"sort"
	"strings"
	"sync"
)

const Mask = "[DISEMBUNYIKAN]"

type Redactor struct {
	mu      sync.RWMutex
	secrets map[string]struct{}
	ordered []string
}

func NewRedactor() *Redactor {
	return &Redactor{secrets: map[string]struct{}{}}
}

var Global = NewRedactor()

func (r *Redactor) Add(secret string) {
	secret = strings.TrimSpace(secret)
	if len(secret) < 4 {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.secrets[secret]; ok {
		return
	}
	r.secrets[secret] = struct{}{}
	r.ordered = append(r.ordered, secret)
	sort.Slice(r.ordered, func(i, j int) bool { return len(r.ordered[i]) > len(r.ordered[j]) })
}

var patterns = []struct {
	re   *regexp.Regexp
	repl string
}{
	{regexp.MustCompile(`(?i)(bearer\s+)[A-Za-z0-9._~+/=\-]+`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)(authorization"?\s*[:=]\s*"?)[^"\s,}]+(\s+[^"\s,}]+)?`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)(define\(\s*['"]DB_PASSWORD['"]\s*,\s*)(['"])[^'"]*(['"])`), "${1}${2}" + Mask + "${3}"},
	{regexp.MustCompile(`(?i)([A-Za-z0-9_\-]*(?:password|passwd|pwd|token|secret|api_key|apikey|api-key)"?\s*[=:]\s*)("[^"]*"|'[^']*'|[^\s,;&}]+)`), "${1}" + Mask},
	{regexp.MustCompile(`(\s-p)[^\s'"]+`), "${1}" + Mask},
	{regexp.MustCompile(`(?i)(://[^:/@\s]+:)[^@\s]+(@)`), "${1}" + Mask + "${2}"},
}

func (r *Redactor) String(s string) string {
	if s == "" {
		return s
	}
	r.mu.RLock()
	for _, secret := range r.ordered {
		if strings.Contains(s, secret) {
			s = strings.ReplaceAll(s, secret, Mask)
		}
	}
	r.mu.RUnlock()
	for _, p := range patterns {
		s = p.re.ReplaceAllString(s, p.repl)
	}
	return s
}

var sensitiveKey = regexp.MustCompile(`(?i)(password|passwd|pwd|token|secret|authorization|api_key|apikey|cookie|private_key|env_content)`)

func IsSensitiveKey(key string) bool {
	return sensitiveKey.MatchString(key)
}
