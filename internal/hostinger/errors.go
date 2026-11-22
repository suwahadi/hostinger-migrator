package hostinger

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

type ErrorKind int

const (
	KindClient ErrorKind = iota
	KindUnauthorized
	KindNotFound
	KindConflict
	KindRateLimited
	KindServer
	KindNetwork
	KindUncertain
)

var (
	ErrUnauthorized = errors.New("token ditolak (401/403)")
	ErrNotFound     = errors.New("resource tidak ditemukan (404)")
	ErrConflict     = errors.New("konflik resource (409)")
	ErrRateLimited  = errors.New("batas rate API terlampaui (429)")
	ErrServer       = errors.New("server Hostinger bermasalah (5xx)")
	ErrNetwork      = errors.New("gangguan jaringan")
	ErrUncertain    = errors.New("hasil operasi tidak pasti")
)

type APIError struct {
	Endpoint string
	Method   string
	Status   int
	Kind     ErrorKind
	Message  string
	Attempts int
	Cause    error
}

func (e *APIError) Error() string {
	var b strings.Builder
	switch e.Kind {
	case KindUnauthorized:
		b.WriteString("token ditolak Hostinger (dicabut, kedaluwarsa, atau tanpa izin)")
	case KindNotFound:
		b.WriteString("resource tidak ditemukan")
	case KindConflict:
		b.WriteString("konflik: resource sudah ada atau sedang diproses")
	case KindRateLimited:
		b.WriteString("batas rate API terlampaui")
	case KindServer:
		b.WriteString("server Hostinger mengembalikan error")
	case KindNetwork:
		b.WriteString("gangguan jaringan")
	case KindUncertain:
		b.WriteString("hasil operasi tidak pasti, periksa dulu apakah resource sudah terbentuk sebelum mengulang")
	default:
		b.WriteString("permintaan ditolak Hostinger")
	}
	fmt.Fprintf(&b, " [%s", e.Endpoint)
	if e.Status > 0 {
		fmt.Fprintf(&b, ", HTTP %d", e.Status)
	}
	if e.Attempts > 1 {
		fmt.Fprintf(&b, ", %d percobaan", e.Attempts)
	}
	b.WriteString("]")
	if e.Message != "" {
		b.WriteString(": ")
		b.WriteString(e.Message)
	}
	if e.Cause != nil && e.Kind == KindNetwork {
		b.WriteString(": ")
		b.WriteString(e.Cause.Error())
	}
	return b.String()
}

func (e *APIError) Unwrap() []error {
	var out []error
	switch e.Kind {
	case KindUnauthorized:
		out = append(out, ErrUnauthorized)
	case KindNotFound:
		out = append(out, ErrNotFound)
	case KindConflict:
		out = append(out, ErrConflict)
	case KindRateLimited:
		out = append(out, ErrRateLimited)
	case KindServer:
		out = append(out, ErrServer)
	case KindNetwork:
		out = append(out, ErrNetwork)
	case KindUncertain:
		out = append(out, ErrUncertain)
	}
	if e.Cause != nil {
		out = append(out, e.Cause)
	}
	return out
}

func IsBlocking(err error) bool {
	return errors.Is(err, ErrUnauthorized)
}

func extractMessage(body []byte) string {
	if len(body) == 0 {
		return ""
	}
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return truncate(strings.TrimSpace(string(body)), 200)
	}
	var parts []string
	for _, key := range []string{"message", "error"} {
		if v, ok := payload[key].(string); ok && v != "" {
			parts = append(parts, v)
			break
		}
	}
	if errs, ok := payload["errors"].(map[string]any); ok {
		keys := make([]string, 0, len(errs))
		for k := range errs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			switch v := errs[k].(type) {
			case []any:
				for _, m := range v {
					parts = append(parts, fmt.Sprintf("%s: %v", k, m))
				}
			default:
				parts = append(parts, fmt.Sprintf("%s: %v", k, v))
			}
		}
	}
	msg := truncate(strings.Join(parts, "; "), 300)
	if id, ok := payload["correlation_id"].(string); ok && id != "" {
		if msg == "" {
			return "correlation_id " + id
		}
		return msg + " (correlation_id " + id + ")"
	}
	return msg
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
