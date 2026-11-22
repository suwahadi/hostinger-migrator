package fakehostinger

import (
	"encoding/json"
	"fmt"
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
	OrderUsers       map[int64]string
	SiteRoot         func(username, domain string) string
	OnCreateDB       func(username, name, user, password string) error
	OnboardingPolls  int
	Fail             map[string]int
	ZoneWrites       []map[string]any
	Snapshots        map[string][]map[string]any
	SSL              map[string]string
	SSLPolls         int
	sslPolls         map[string]int
	freeCounter      int
	onboardings      map[string]string
	pollCount        map[string]int
	hits             map[string]int
	writes           int
}

func (s *Server) Lock()   { s.mu.Lock() }
func (s *Server) Unlock() { s.mu.Unlock() }

func (s *Server) SetFail(key string, status int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Fail == nil {
		s.Fail = map[string]int{}
	}
	s.Fail[key] = status
}

func (s *Server) failFor(key string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Fail[key]
}

func (s *Server) WebsitesOf(token string) []hostinger.Website {
	s.mu.Lock()
	defer s.mu.Unlock()
	if ws, ok := s.WebsitesFor[token]; ok {
		return append([]hostinger.Website(nil), ws...)
	}
	return append([]hostinger.Website(nil), s.Websites...)
}

func New(tls bool, validTokens ...string) *Server {
	s := &Server{valid: map[string]bool{}, hits: map[string]int{}, WebsitesFor: map[string][]hostinger.Website{}, OrdersFor: map[string][]hostinger.Order{},
		OrderUsers: map[int64]string{}, onboardings: map[string]string{}, pollCount: map[string]int{}, Fail: map[string]int{},
		Snapshots: map[string][]map[string]any{}, SSL: map[string]string{}, sslPolls: map[string]int{}}
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
		s.serveWrite(w, r, token)
		return
	}
	switch r.URL.Path {
	case "/api/hosting/v1/onboardings":
		d := r.URL.Query().Get("domain")
		s.mu.Lock()
		st, ok := s.onboardings[d]
		if ok && st == "running" {
			s.pollCount[d]++
			if s.pollCount[d] >= s.OnboardingPolls {
				s.onboardings[d] = "completed"
				st = "completed"
			}
		}
		s.mu.Unlock()
		if !ok {
			w.Write([]byte("[]"))
			return
		}
		json.NewEncoder(w).Encode([]map[string]any{{"domain": d, "username": "", "status": st, "created_at": "2026-11-22T00:00:00Z", "updated_at": "2026-11-22T00:00:00Z"}})
		return
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
		d := strings.TrimPrefix(path, "/api/dns/v1/snapshots/")
		s.mu.Lock()
		_, ok := s.Zones[d]
		list := []map[string]any{{"id": 1, "reason": "awal", "created_at": "2026-11-01T00:00:00Z"}}
		for _, sn := range s.Snapshots[d] {
			list = append(list, map[string]any{"id": sn["id"], "reason": sn["reason"], "created_at": "2026-11-22T00:00:00Z"})
		}
		s.mu.Unlock()
		if !ok {
			notFound(w)
			return
		}
		json.NewEncoder(w).Encode(list)
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
		case len(parts) == 5 && parts[1] == "websites" && parts[3] == "ssl" && parts[4] == "status":
			s.mu.Lock()
			st := s.SSL[parts[2]]
			if st == "installing" {
				s.sslPolls[parts[2]]++
				if s.sslPolls[parts[2]] >= s.SSLPolls {
					st = "active"
					s.SSL[parts[2]] = st
				}
			}
			s.mu.Unlock()
			if st == "" {
				st = "not_installed"
			}
			json.NewEncoder(w).Encode(map[string]any{"status": st, "provider": nil, "is_lifetime": false, "is_https_redirect_enabled": false, "expires_at": nil, "last_error": nil})
			return
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

func (s *Server) serveWrite(w http.ResponseWriter, r *http.Request, token string) {
	var body map[string]any
	json.NewDecoder(r.Body).Decode(&body)
	path := r.URL.Path
	fail := func(key string) bool {
		if code := s.failFor(key); code != 0 {
			w.WriteHeader(code)
			json.NewEncoder(w).Encode(map[string]string{"message": "gagal disimulasikan: " + key})
			return true
		}
		return false
	}
	switch {
	case r.Method == http.MethodPost && path == "/api/hosting/v1/domains/free-subdomains":
		if fail("free-subdomain") {
			return
		}
		s.mu.Lock()
		s.freeCounter++
		d := fmt.Sprintf("dummy-%d.hostingersite.com", s.freeCounter)
		s.mu.Unlock()
		json.NewEncoder(w).Encode(map[string]string{"domain": d})
		return
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/hosting/v1/orders/") && strings.HasSuffix(path, "/onboardings"):
		if fail("onboarding") {
			return
		}
		id, _ := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(path, "/api/hosting/v1/orders/"), "/onboardings"), 10, 64)
		domain, _ := body["domain"].(string)
		s.mu.Lock()
		user := s.OrderUsers[id]
		if _, exists := s.onboardings[domain]; exists {
			s.mu.Unlock()
			w.WriteHeader(http.StatusConflict)
			w.Write([]byte(`{"message":"sudah ada"}`))
			return
		}
		status := "completed"
		if s.OnboardingPolls > 0 {
			status = "running"
		}
		s.onboardings[domain] = status
		root := ""
		if s.SiteRoot != nil {
			root = s.SiteRoot(user, domain)
		}
		site := hostinger.Website{Domain: domain, Username: user, OrderID: id, VhostType: "addon", IsEnabled: true, RootDirectory: root, WebsiteType: "other"}
		if _, ok := s.WebsitesFor[token]; ok {
			s.WebsitesFor[token] = append(s.WebsitesFor[token], site)
		} else {
			s.Websites = append(s.Websites, site)
		}
		s.mu.Unlock()
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(map[string]any{"domain": domain, "username": user, "status": status, "created_at": "x", "updated_at": "x"})
		return
	case strings.HasPrefix(path, "/api/dns/v1/zones/") && (r.Method == http.MethodPut || r.Method == http.MethodDelete):
		if fail("dns") {
			return
		}
		d := strings.TrimPrefix(path, "/api/dns/v1/zones/")
		s.mu.Lock()
		defer s.mu.Unlock()
		prev, _ := json.Marshal(s.Zones[d])
		var prevCopy []map[string]any
		json.Unmarshal(prev, &prevCopy)
		s.Snapshots[d] = append(s.Snapshots[d], map[string]any{"id": len(s.Snapshots[d]) + 2, "reason": "perubahan " + r.Method, "zone": prevCopy})
		zone := s.Zones[d]
		if r.Method == http.MethodPut {
			items, _ := body["zone"].([]any)
			overwrite := true
			if v, ok := body["overwrite"].(bool); ok {
				overwrite = v
			}
			for _, raw := range items {
				rr, _ := raw.(map[string]any)
				if overwrite {
					zone = removeRR(zone, rr["name"], rr["type"])
				}
				zone = append(zone, rr)
			}
		} else {
			filters, _ := body["filters"].([]any)
			for _, raw := range filters {
				f, _ := raw.(map[string]any)
				zone = removeRR(zone, f["name"], f["type"])
			}
		}
		s.Zones[d] = zone
		s.ZoneWrites = append(s.ZoneWrites, map[string]any{"domain": d, "method": r.Method, "body": body})
		w.Write([]byte(`{"message":"ok"}`))
		return
	case r.Method == http.MethodPost && strings.HasPrefix(path, "/api/dns/v1/snapshots/") && strings.HasSuffix(path, "/restore"):
		parts := strings.Split(strings.TrimPrefix(path, "/api/dns/v1/snapshots/"), "/")
		id, _ := strconv.Atoi(parts[1])
		s.mu.Lock()
		defer s.mu.Unlock()
		for _, sn := range s.Snapshots[parts[0]] {
			if sn["id"] == id {
				zone, _ := sn["zone"].([]map[string]any)
				s.Zones[parts[0]] = zone
				w.Write([]byte(`{"message":"ok"}`))
				return
			}
		}
		notFound(w)
		return
	case strings.HasPrefix(path, "/api/hosting/v1/accounts/"):
		parts := strings.Split(strings.TrimPrefix(path, "/api/hosting/v1/accounts/"), "/")
		user := parts[0]
		switch {
		case r.Method == http.MethodPost && len(parts) == 4 && parts[1] == "websites" && parts[3] == "parked-domains":
			if fail("parked") {
				return
			}
			pd, _ := body["parked_domain"].(string)
			s.mu.Lock()
			s.Parked[parts[2]] = append(s.Parked[parts[2]], map[string]any{"domain": pd, "parent_domain": parts[2], "type": "domain"})
			s.mu.Unlock()
			w.Write([]byte(`{"message":"ok"}`))
			return
		case r.Method == http.MethodDelete && len(parts) == 5 && parts[1] == "websites" && parts[3] == "parked-domains":
			s.mu.Lock()
			var keep []map[string]any
			for _, p := range s.Parked[parts[2]] {
				if p["domain"] != parts[4] {
					keep = append(keep, p)
				}
			}
			s.Parked[parts[2]] = keep
			s.mu.Unlock()
			w.Write([]byte(`{"message":"ok"}`))
			return
		case r.Method == http.MethodPost && len(parts) == 5 && parts[1] == "websites" && parts[3] == "ssl" && parts[4] == "setup":
			if fail("ssl") {
				return
			}
			s.mu.Lock()
			s.SSL[parts[2]] = "installing"
			s.mu.Unlock()
			w.Write([]byte(`{"message":"ok"}`))
			return
		case r.Method == http.MethodPost && len(parts) == 2 && parts[1] == "databases":
			if fail("db-create") {
				return
			}
			name, _ := body["name"].(string)
			dbUser, _ := body["user"].(string)
			pw, _ := body["password"].(string)
			if !strings.HasPrefix(name, user+"_") {
				name = user + "_" + name
			}
			if s.OnCreateDB != nil {
				if err := s.OnCreateDB(user, name, dbUser, pw); err != nil {
					w.WriteHeader(http.StatusInternalServerError)
					return
				}
			}
			s.mu.Lock()
			s.Databases[user] = append(s.Databases[user], map[string]any{"name": name, "user": dbUser, "domain": body["website_domain"]})
			s.mu.Unlock()
			w.Write([]byte(`{"message":"ok"}`))
			return
		case r.Method == http.MethodPatch && len(parts) == 5 && parts[3] == "php" && parts[4] == "version":
			if fail("php") {
				return
			}
			v, _ := body["version"].(string)
			s.mu.Lock()
			s.PHP[parts[2]] = v
			s.mu.Unlock()
			w.Write([]byte(`{"message":"ok"}`))
			return
		}
	}
	w.WriteHeader(http.StatusNotFound)
	w.Write([]byte(`{"message":"endpoint tulis tidak disimulasikan"}`))
}

func removeRR(zone []map[string]any, name, typ any) []map[string]any {
	var out []map[string]any
	for _, rr := range zone {
		if rr["name"] == name && rr["type"] == typ {
			continue
		}
		out = append(out, rr)
	}
	return out
}

func (s *Server) Zone(domain string) []map[string]any {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, _ := json.Marshal(s.Zones[domain])
	var out []map[string]any
	json.Unmarshal(b, &out)
	return out
}
