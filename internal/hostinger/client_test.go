package hostinger

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"
)

const testToken = "FAKE-test-token-0123456789"

type sleeper struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (s *sleeper) sleep(ctx context.Context, d time.Duration) error {
	s.mu.Lock()
	s.waits = append(s.waits, d)
	s.mu.Unlock()
	return ctx.Err()
}

func newTestClient(t *testing.T, h http.Handler, retryMax int) (*Client, *sleeper) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	sl := &sleeper{}
	lim := NewLimiter(60000)
	lim.SetClock(nil, sl.sleep)
	c, err := NewClient(Options{
		Token:    testToken,
		BaseURL:  srv.URL,
		RetryMax: retryMax,
		Limiter:  lim,
		Sleep:    sl.sleep,
		Jitter:   func() float64 { return 1 },
	})
	if err != nil {
		t.Fatal(err)
	}
	return c, sl
}

func TestRegistryLoadsAllContracts(t *testing.T) {
	reg, err := DefaultRegistry()
	if err != nil {
		t.Fatal(err)
	}
	if n := len(reg.IDs()); n != 65 {
		t.Fatalf("jumlah endpoint = %d, harus 65", n)
	}
	seen := map[string]string{}
	for _, id := range reg.IDs() {
		ep := reg.byID[id]
		key := ep.Method + " " + ep.BaseURL + ep.Path
		if other, dup := seen[key]; dup {
			t.Errorf("endpoint ganda %s dan %s", id, other)
		}
		seen[key] = id
	}
	ep, err := reg.Lookup("hosting.websites.list")
	if err != nil || ep.Method != http.MethodGet || ep.BaseURL != DefaultBaseURL || ep.Auth != AuthHPanel {
		t.Fatalf("lookup salah: %+v %v", ep, err)
	}
	mail, _ := reg.Lookup("mailapi.me")
	if mail.BaseURL != DefaultMailBaseURL || mail.Auth != AuthMailAPI {
		t.Fatalf("mail api salah: %+v", mail)
	}
}

func TestRegistryRejectsUnknownAndDeprecated(t *testing.T) {
	reg, _ := DefaultRegistry()
	if _, err := reg.Lookup("hosting.karangan.sendiri"); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("err = %v", err)
	}
	if _, err := reg.Lookup("hosting.websites.create"); !errors.Is(err, ErrEndpointDeprecated) {
		t.Fatalf("err = %v", err)
	}
}

func TestRegistryRejectsAgencyHosting(t *testing.T) {
	fsys := fstest.MapFS{"contracts/x.yaml": {Data: []byte(`
sumber:
  base_url: https://developers.hostinger.com
  autentikasi: bearer_token_hpanel
endpoint:
  - id: agency.domain
    operation_id: agency-hosting_changeWebsiteDomainV1
    method: PUT
    path: /api/agency-hosting/v1/websites/{website_uid}/domains/{from_domain}
    status_spesifikasi: tersedia
`)}}
	if _, err := LoadRegistry(fsys); err == nil {
		t.Fatal("namespace agency-hosting harus ditolak")
	}
}

func TestClientRefusesUnknownEndpoint(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { atomic.AddInt32(&hits, 1) }), 0)
	if _, err := c.Do(context.Background(), Request{Endpoint: "tidak.ada"}, nil); !errors.Is(err, ErrUnknownEndpoint) {
		t.Fatalf("err = %v", err)
	}
	if hits != 0 {
		t.Fatal("tidak boleh ada request ke server")
	}
}

func TestClientRefusesMailAPIWithHPanelToken(t *testing.T) {
	c, _ := newTestClient(t, http.NotFoundHandler(), 0)
	if _, err := c.Do(context.Background(), Request{Endpoint: "mailapi.me"}, nil); err == nil {
		t.Fatal("token hPanel tidak boleh dipakai untuk Mail API")
	}
}

func TestClientSendsBearerAndEscapesPath(t *testing.T) {
	var gotAuth, gotPath, gotUA string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.EscapedPath()
		gotUA = r.Header.Get("User-Agent")
		w.Write([]byte(`{"status":"active"}`))
	}), 0)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.ssl.status", PathParams: map[string]string{"username": "u123", "domain": "a b.com"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if gotAuth != "Bearer "+testToken {
		t.Fatalf("auth = %q", gotAuth)
	}
	if gotPath != "/api/hosting/v1/accounts/u123/websites/a%20b.com/ssl/status" {
		t.Fatalf("path = %s", gotPath)
	}
	if gotUA == "" {
		t.Fatal("user agent kosong")
	}
}

func TestClientMissingPathParam(t *testing.T) {
	c, _ := newTestClient(t, http.NotFoundHandler(), 0)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.ssl.status", PathParams: map[string]string{"username": "u1"}}, nil)
	if err == nil {
		t.Fatal("parameter path kurang harus ditolak")
	}
}

func TestClient401IsBlockingWithoutRetry(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthenticated."}`))
	}), 8)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if !errors.Is(err, ErrUnauthorized) || !IsBlocking(err) {
		t.Fatalf("err = %v", err)
	}
	if hits != 1 {
		t.Fatalf("401 tidak boleh diulang, hits = %d", hits)
	}
}

func TestClient403IsBlocking(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}), 3)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if !IsBlocking(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestClient429HonorsRetryAfter(t *testing.T) {
	var hits int32
	c, sl := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte(`{"data":[],"meta":{"current_page":1,"per_page":100,"total":0}}`))
	}), 8)
	if _, err := c.ListOrders(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hits != 2 {
		t.Fatalf("hits = %d", hits)
	}
	found := false
	for _, w := range sl.waits {
		if w >= 6*time.Second && w <= 7*time.Second {
			found = true
		}
	}
	if !found {
		t.Fatalf("Retry-After 7 detik tidak dihormati: %v", sl.waits)
	}
}

func TestClient429GivesUpAfterRetryMax(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusTooManyRequests)
	}), 2)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
	if hits != 3 {
		t.Fatalf("hits = %d", hits)
	}
}

func TestClient5xxRetriesGetLimited(t *testing.T) {
	var hits int32
	c, sl := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusBadGateway)
	}), 8)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v", err)
	}
	if hits != maxServerRetries+1 {
		t.Fatalf("hits = %d", hits)
	}
	var backoffs []time.Duration
	for _, w := range sl.waits {
		if w >= 500*time.Millisecond {
			backoffs = append(backoffs, w)
		}
	}
	if len(backoffs) != maxServerRetries || backoffs[0] != time.Second || backoffs[1] != 2*time.Second || backoffs[2] != 4*time.Second {
		t.Fatalf("backoff eksponensial salah: %v", backoffs)
	}
}

func TestClient5xxThenSuccess(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&hits, 1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`{"status":"active"}`))
	}), 8)
	var out struct{ Status string }
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.ssl.status", PathParams: map[string]string{"username": "u1", "domain": "a.com"}}, &out)
	if err != nil || out.Status != "active" {
		t.Fatalf("err = %v out = %+v", err, out)
	}
}

func TestClientPost5xxIsUncertainWithoutRetry(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}), 8)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.free_subdomains.generate"}, nil)
	if !errors.Is(err, ErrUncertain) {
		t.Fatalf("err = %v", err)
	}
	if hits != 1 {
		t.Fatalf("POST tidak boleh diulang otomatis, hits = %d", hits)
	}
}

func TestClientNetworkErrorRetriesThenFails(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	sl := &sleeper{}
	lim := NewLimiter(60000)
	lim.SetClock(nil, sl.sleep)
	c, _ := NewClient(Options{Token: testToken, BaseURL: url, RetryMax: 3, Limiter: lim, Sleep: sl.sleep, Jitter: func() float64 { return 0 }})
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if !errors.Is(err, ErrNetwork) {
		t.Fatalf("err = %v", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Attempts != 4 {
		t.Fatalf("percobaan salah: %+v", apiErr)
	}
}

func TestClientAcceptedIsNotResent(t *testing.T) {
	var hits int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusAccepted)
	}), 8)
	resp, err := c.Do(context.Background(), Request{Endpoint: "hosting.ssl.setup", PathParams: map[string]string{"username": "u1", "domain": "a.com"}}, nil)
	if err != nil || !resp.Accepted || hits != 1 {
		t.Fatalf("resp = %+v err = %v hits = %d", resp, err, hits)
	}
}

func TestClientErrorMessageFromBody(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"message":"Data tidak valid","errors":{"domain":["Domain sudah dipakai"]}}`))
	}), 0)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.free_subdomains.generate"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Domain sudah dipakai") || !strings.Contains(err.Error(), "HTTP 422") {
		t.Fatalf("err = %v", err)
	}
}

func TestListAllPaginates(t *testing.T) {
	total := 250
	var pages []int
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		per, _ := strconv.Atoi(r.URL.Query().Get("per_page"))
		pages = append(pages, page)
		var data []Website
		for i := (page - 1) * per; i < page*per && i < total; i++ {
			data = append(data, Website{Domain: fmt.Sprintf("web%d.com", i), Username: "u1"})
		}
		json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]int{"current_page": page, "per_page": per, "total": total}})
	}), 0)
	sites, err := c.ListWebsites(context.Background(), WebsiteFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(sites) != total || len(pages) != 3 {
		t.Fatalf("sites = %d pages = %v", len(sites), pages)
	}
}

func TestLimiterSpacesRequests(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sl := &sleeper{}
	l := NewLimiter(60)
	l.SetClock(func() time.Time { return now }, sl.sleep)
	for i := 0; i < 3; i++ {
		l.Wait(context.Background())
	}
	if len(sl.waits) != 2 || sl.waits[0] != time.Second || sl.waits[1] != 2*time.Second {
		t.Fatalf("waits = %v", sl.waits)
	}
}

func TestLimiterBurstThenSpaces(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sl := &sleeper{}
	l := NewLimiterWithBurst(60, 3)
	l.SetClock(func() time.Time { return now }, sl.sleep)
	for i := 0; i < 5; i++ {
		l.Wait(context.Background())
	}
	if len(sl.waits) != 2 || sl.waits[0] != time.Second || sl.waits[1] != 2*time.Second {
		t.Fatalf("waits = %v", sl.waits)
	}
	now = now.Add(time.Hour)
	sl.waits = nil
	for i := 0; i < 3; i++ {
		l.Wait(context.Background())
	}
	if len(sl.waits) != 0 {
		t.Fatalf("setelah jeda panjang burst harus tersedia lagi: %v", sl.waits)
	}
}

func TestLimiterBurstRespectsPause(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sl := &sleeper{}
	l := NewLimiterWithBurst(60, 5)
	l.SetClock(func() time.Time { return now }, sl.sleep)
	l.PauseUntil(now.Add(10 * time.Second))
	l.Wait(context.Background())
	if len(sl.waits) != 1 || sl.waits[0] != 10*time.Second {
		t.Fatalf("waits = %v", sl.waits)
	}
}

func TestLimiterObserveHeaders(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sl := &sleeper{}
	l := NewLimiter(60000)
	l.SetClock(func() time.Time { return now }, sl.sleep)
	h := http.Header{}
	h.Set("X-RateLimit-Remaining", "0")
	h.Set("X-RateLimit-Reset", "30")
	l.Observe(h)
	l.Wait(context.Background())
	if len(sl.waits) != 1 || sl.waits[0] != 30*time.Second {
		t.Fatalf("waits = %v", sl.waits)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if d, ok := parseRetryAfter("12", now); !ok || d != 12*time.Second {
		t.Fatal("detik salah")
	}
	if d, ok := parseRetryAfter(now.Add(5*time.Second).Format(http.TimeFormat), now); !ok || d != 5*time.Second {
		t.Fatalf("tanggal salah: %v", d)
	}
	if _, ok := parseRetryAfter("abc", now); ok {
		t.Fatal("nilai tidak valid harus ditolak")
	}
}

func TestBuildDiscovery(t *testing.T) {
	orders := []Order{
		{ID: 1, Status: "active", Plan: OrderPlan{Name: "hostinger_premium"}},
		{ID: 2, Status: "active", Plan: OrderPlan{Name: "hostinger_single"}},
		{ID: 3, Status: "active", Plan: OrderPlan{Name: "hostinger_business"}},
	}
	sites := []Website{
		{Domain: "toko.id", Username: "u494983133", OrderID: 1},
		{Domain: "domainanda.com", Username: "u494983133", OrderID: 1},
		{Domain: "contoh.com", Username: "u597522494", OrderID: 2},
		{Domain: "builder.site", Username: "", OrderID: 9},
	}
	d := BuildDiscovery(orders, sites)
	if len(d.Accounts) != 2 {
		t.Fatalf("akun = %+v", d.Accounts)
	}
	a := d.Accounts[0]
	if a.Username != "u494983133" || a.WebsiteCount != 2 || a.Plan != "hostinger_premium" || a.Domains[0] != "domainanda.com" {
		t.Fatalf("akun pertama salah: %+v", a)
	}
	if len(d.OrdersWithoutSite) != 1 || d.OrdersWithoutSite[0].ID != 3 {
		t.Fatalf("order tanpa website salah: %+v", d.OrdersWithoutSite)
	}
}

func TestLimiterObserveIETFHeader(t *testing.T) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sl := &sleeper{}
	l := NewLimiter(60000)
	l.SetClock(func() time.Time { return now }, sl.sleep)
	h := http.Header{}
	h.Set("RateLimit", `"api";r=5;t=40`)
	l.Observe(h)
	l.Wait(context.Background())
	if len(sl.waits) != 0 {
		t.Fatalf("masih ada sisa kuota, tidak boleh menunggu: %v", sl.waits)
	}
	h.Set("RateLimit", `"api";r=0;t=40`)
	l.Observe(h)
	l.Wait(context.Background())
	if len(sl.waits) != 1 || sl.waits[0] != 40*time.Second {
		t.Fatalf("waits = %v", sl.waits)
	}
}

func TestErrorIncludesCorrelationID(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"message":"Unauthenticated.","correlation_id":"a34994c2-dd75"}`))
	}), 0)
	_, err := c.Do(context.Background(), Request{Endpoint: "hosting.orders.list"}, nil)
	if err == nil || !strings.Contains(err.Error(), "Unauthenticated. (correlation_id a34994c2-dd75)") {
		t.Fatalf("err = %v", err)
	}
}
