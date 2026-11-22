package fakehostinger

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"

	"hostinger-migrator/internal/hostinger"
)

type Server struct {
	*httptest.Server
	mu               sync.Mutex
	valid            map[string]bool
	EchoTokenInError bool
	Orders           []hostinger.Order
	Websites         []hostinger.Website
	Zones            map[string][]map[string]any
	Databases        map[string][]map[string]any
	MailOrders       []map[string]any
	MailStatus       int
	Portfolio        []hostinger.PortfolioDomain
	Subdomains       map[string][]map[string]any
	Parked           map[string][]map[string]any
	PHP              map[string]string
	Cron             map[string][]map[string]any
	Files            map[string][]map[string]any
	Mailboxes        map[string][]map[string]any
	WebsitesFor      map[string][]hostinger.Website
	OrdersFor        map[string][]hostinger.Order
	hits             map[string]int
	writes           int
}

func New(tls bool, validTokens ...string) *Server {
	s := &Server{valid: map[string]bool{}, hits: map[string]int{}, WebsitesFor: map[string][]hostinger.Website{}, OrdersFor: map[string][]hostinger.Order{}}
	for _, t := range validTokens {
		s.valid[t] = true
	}
	s.Orders = []hostinger.Order{
		{ID: 101, Status: "active", Plan: hostinger.OrderPlan{Name: "hostinger_premium"}},
		{ID: 102, Status: "active", Plan: hostinger.OrderPlan{Name: "hostinger_single"}},
	}
	s.Websites = []hostinger.Website{
		{Domain: "domainanda.com", Username: "u494983133", OrderID: 101, VhostType: "main", IsEnabled: true, RootDirectory: "/home/u494983133/domains/domainanda.com/public_html", WebsiteType: "wordpress"},
		{Domain: "toko.id", Username: "u494983133", OrderID: 101, VhostType: "addon", IsEnabled: true, RootDirectory: "/home/u494983133/domains/toko.id/public_html", WebsiteType: "other"},
		{Domain: "contoh.com", Username: "u597522494", OrderID: 102, VhostType: "main", IsEnabled: false, RootDirectory: "/home/u597522494/domains/contoh.com/public_html", WebsiteType: "other"},
	}
	s.Zones = map[string][]map[string]any{
		"domainanda.com": {
			{"name": "@", "type": "A", "ttl": 14400, "records": []map[string]any{{"content": "1.2.3.4"}}},
			{"name": "@", "type": "MX", "ttl": 14400, "records": []map[string]any{{"content": "10 mx1.hostinger.com"}}},
		},
	}
	s.Databases = map[string][]map[string]any{
		"u494983133": {{"name": "u494983133_wp", "user": "u494983133_wp", "domain": "domainanda.com"}},
	}
	s.MailOrders = []map[string]any{{"id": "mo-1", "status": "active", "domain": map[string]any{"id": "d1", "name": "domainanda.com"}, "plan": map[string]any{"name": "hostinger_free", "title": "Free Email"}}}
	s.Mailboxes = map[string][]map[string]any{
		"mo-1": {
			{"id": "mb-1", "address": "info@domainanda.com", "status": "active", "usage": map[string]any{"storage_used": 2048, "storage_quota": 1048576, "messages_used": 120}},
			{"id": "mb-2", "address": "admin@domainanda.com", "status": "active", "usage": map[string]any{"storage_used": 512, "storage_quota": 1048576, "messages_used": 15}},
		},
	}
	s.Portfolio = []hostinger.PortfolioDomain{
		{ID: 1, Domain: "domainanda.com", Type: "domain", Status: "active", ExpiresAt: "2027-05-01T00:00:00Z"},
		{ID: 2, Domain: "contoh.com", Type: "domain", Status: "expired", ExpiresAt: "2026-01-01T00:00:00Z"},
	}
	s.Subdomains = map[string][]map[string]any{"domainanda.com": {{"domain": "blog.domainanda.com", "parent_domain": "domainanda.com", "root_directory": "/home/u494983133/domains/domainanda.com/public_html/blog", "subdomain": "blog"}}}
	s.Parked = map[string][]map[string]any{}
	s.PHP = map[string]string{"domainanda.com": "8.2", "toko.id": "8.1", "contoh.com": "7.4"}
	s.Cron = map[string][]map[string]any{"u494983133": {{"uid": "c1", "username": "u494983133", "time": "*/5 * * * *", "command": "php /home/u494983133/domains/toko.id/artisan schedule:run"}}}
	s.Files = map[string][]map[string]any{"contoh.com": {{"name": "index.html", "path": "index.html", "type": "file", "size_bytes": 100}}}
	h := http.HandlerFunc(s.serve)
	if tls {
		s.Server = httptest.NewTLSServer(h)
	} else {
		s.Server = httptest.NewServer(h)
	}
	return s
}

func (s *Server) Allow(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.valid[token] = true
}

func (s *Server) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.valid, token)
}

func (s *Server) Writes() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writes
}

func (s *Server) Hits(path string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.hits[path]
}

func (s *Server) serve(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	s.hits[r.URL.Path]++
	token := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	ok := s.valid[token]
	echo := s.EchoTokenInError
	s.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	if !ok {
		w.WriteHeader(http.StatusUnauthorized)
		msg := "Unauthenticated."
		if echo {
			msg += " Token " + token + " tidak valid"
		}
		json.NewEncoder(w).Encode(map[string]string{"message": msg})
		return
	}
	if r.Method != http.MethodGet {
		s.mu.Lock()
		s.writes++
		s.mu.Unlock()
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/api/hosting/v1/orders":
		if o, ok := s.OrdersFor[token]; ok {
			writePage(w, r, o)
			return
		}
		writePage(w, r, s.Orders)
	case "/api/hosting/v1/websites":
		sites := s.Websites
		if ws, ok := s.WebsitesFor[token]; ok {
			sites = ws
		}
		if u := r.URL.Query().Get("username"); u != "" {
			all := sites
			sites = nil
			for _, x := range all {
				if x.Username == u {
					sites = append(sites, x)
				}
			}
		}
		writePage(w, r, sites)
	case "/api/mail/v1/orders":
		if s.MailStatus != 0 {
			w.WriteHeader(s.MailStatus)
			w.Write([]byte(`{"message":"Forbidden"}`))
			return
		}
		writePage(w, r, s.MailOrders)
	default:
		s.serveDynamic(w, r)
	}
}

func (s *Server) serveDynamic(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path
	switch {
	case strings.HasPrefix(path, "/api/dns/v1/zones/"):
		zone, ok := s.Zones[strings.TrimPrefix(path, "/api/dns/v1/zones/")]
		if !ok {
			notFound(w)
			return
		}
		json.NewEncoder(w).Encode(zone)
		return
	case strings.HasPrefix(path, "/api/dns/v1/snapshots/"):
		if _, ok := s.Zones[strings.TrimPrefix(path, "/api/dns/v1/snapshots/")]; !ok {
			notFound(w)
			return
		}
		w.Write([]byte(`[{"id":1,"reason":"awal","created_at":"2026-11-01T00:00:00Z"}]`))
		return
	case path == "/api/domains/v1/portfolio":
		json.NewEncoder(w).Encode(s.Portfolio)
		return
	case strings.HasPrefix(path, "/api/mail/v1/orders/") && strings.HasSuffix(path, "/mailboxes"):
		id := strings.TrimSuffix(strings.TrimPrefix(path, "/api/mail/v1/orders/"), "/mailboxes")
		writePage(w, r, s.Mailboxes[id])
		return
	case strings.HasPrefix(path, "/api/hosting/v1/accounts/"):
		parts := strings.Split(strings.TrimPrefix(path, "/api/hosting/v1/accounts/"), "/")
		user := parts[0]
		switch {
		case len(parts) == 2 && parts[1] == "databases":
			writePage(w, r, s.Databases[user])
			return
		case len(parts) == 2 && parts[1] == "cron-jobs":
			writeJSON(w, s.Cron[user])
			return
		case len(parts) == 4 && parts[1] == "websites" && parts[3] == "subdomains":
			writeJSON(w, s.Subdomains[parts[2]])
			return
		case len(parts) == 4 && parts[1] == "websites" && parts[3] == "parked-domains":
			writeJSON(w, s.Parked[parts[2]])
			return
		case len(parts) == 5 && parts[1] == "websites" && parts[3] == "php" && parts[4] == "details":
			if v, ok := s.PHP[parts[2]]; ok {
				json.NewEncoder(w).Encode(map[string]any{"php_version": v, "php_version_full": v + ".0"})
				return
			}
		case len(parts) == 4 && parts[1] == "domains" && parts[3] == "files":
			if items, ok := s.Files[parts[2]]; ok {
				json.NewEncoder(w).Encode(map[string]any{"path": "", "items": items, "total_items": len(items), "total_items_current_page": len(items), "offset": 0})
				return
			}
		}
	}
	notFound(w)
}

func writeJSON(w http.ResponseWriter, v []map[string]any) {
	if v == nil {
		v = []map[string]any{}
	}
	json.NewEncoder(w).Encode(v)
}

func notFound(w http.ResponseWriter) {
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"message":"Not found"}`))
}

func writePage[T any](w http.ResponseWriter, r *http.Request, all []T) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
	if page < 1 {
		page = 1
	}
	if per < 1 {
		per = 25
	}
	start := (page - 1) * per
	end := start + per
	if start > len(all) {
		start = len(all)
	}
	if end > len(all) {
		end = len(all)
	}
	json.NewEncoder(w).Encode(map[string]any{
		"data": all[start:end],
		"meta": map[string]int{"current_page": page, "per_page": per, "total": len(all)},
	})
}
