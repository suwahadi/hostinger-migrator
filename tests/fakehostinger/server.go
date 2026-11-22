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
	hits             map[string]int
}

func New(tls bool, validTokens ...string) *Server {
	s := &Server{valid: map[string]bool{}, hits: map[string]int{}}
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
	h := http.HandlerFunc(s.serve)
	if tls {
		s.Server = httptest.NewTLSServer(h)
	} else {
		s.Server = httptest.NewServer(h)
	}
	return s
}

func (s *Server) Revoke(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.valid, token)
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
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	switch r.URL.Path {
	case "/api/hosting/v1/orders":
		writePage(w, r, s.Orders)
	case "/api/hosting/v1/websites":
		writePage(w, r, s.Websites)
	default:
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte(`{"message":"Not found"}`))
	}
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
