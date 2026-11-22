package hostinger

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	DefaultBaseURL     = "https://developers.hostinger.com"
	DefaultMailBaseURL = "https://api.mail.hostinger.com"
	maxServerRetries   = 3
	maxBodyBytes       = 32 << 20
)

type Options struct {
	Token       string
	Auth        string
	BaseURL     string
	MailBaseURL string
	Timeout     time.Duration
	RetryMax    int
	Limiter     *Limiter
	HTTPClient  *http.Client
	Registry    *Registry
	Logger      *slog.Logger
	UserAgent   string
	Sleep       func(context.Context, time.Duration) error
	Now         func() time.Time
	Jitter      func() float64
}

type Client struct {
	token       string
	auth        string
	baseURL     string
	mailBaseURL string
	retryMax    int
	limiter     *Limiter
	http        *http.Client
	registry    *Registry
	log         *slog.Logger
	userAgent   string
	sleep       func(context.Context, time.Duration) error
	now         func() time.Time
	jitter      func() float64
}

type Request struct {
	Endpoint   string
	PathParams map[string]string
	Query      url.Values
	Body       any
}

type Response struct {
	Status   int
	Header   http.Header
	Body     []byte
	Accepted bool
	Attempts int
}

func NewClient(opts Options) (*Client, error) {
	if opts.Token == "" {
		return nil, errors.New("token API kosong")
	}
	reg := opts.Registry
	if reg == nil {
		var err error
		reg, err = DefaultRegistry()
		if err != nil {
			return nil, err
		}
	}
	c := &Client{
		token:       opts.Token,
		auth:        opts.Auth,
		baseURL:     strings.TrimRight(opts.BaseURL, "/"),
		mailBaseURL: strings.TrimRight(opts.MailBaseURL, "/"),
		retryMax:    opts.RetryMax,
		limiter:     opts.Limiter,
		http:        opts.HTTPClient,
		registry:    reg,
		log:         opts.Logger,
		userAgent:   opts.UserAgent,
		sleep:       opts.Sleep,
		now:         opts.Now,
		jitter:      opts.Jitter,
	}
	if c.auth == "" {
		c.auth = AuthHPanel
	}
	if c.baseURL == "" {
		c.baseURL = DefaultBaseURL
	}
	if c.mailBaseURL == "" {
		c.mailBaseURL = DefaultMailBaseURL
	}
	if c.retryMax < 0 {
		c.retryMax = 0
	}
	if c.limiter == nil {
		c.limiter = NewLimiter(60)
	}
	if c.http == nil {
		timeout := opts.Timeout
		if timeout <= 0 {
			timeout = 60 * time.Second
		}
		c.http = &http.Client{Timeout: timeout}
	}
	if c.log == nil {
		c.log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if c.userAgent == "" {
		c.userAgent = "hostinger-migrator"
	}
	if c.sleep == nil {
		c.sleep = sleepCtx
	}
	if c.now == nil {
		c.now = time.Now
	}
	if c.jitter == nil {
		c.jitter = rand.Float64
	}
	return c, nil
}

func (c *Client) endpointURL(ep Endpoint, req Request) (string, error) {
	base := ep.BaseURL
	switch base {
	case DefaultBaseURL:
		base = c.baseURL
	case DefaultMailBaseURL:
		base = c.mailBaseURL
	}
	path := ep.Path
	for k, v := range req.PathParams {
		if v == "" {
			return "", fmt.Errorf("parameter path %s kosong untuk %s", k, ep.ID)
		}
		path = strings.ReplaceAll(path, "{"+k+"}", url.PathEscape(v))
	}
	if strings.ContainsAny(path, "{}") {
		return "", fmt.Errorf("parameter path belum lengkap untuk %s: %s", ep.ID, path)
	}
	u := base + path
	if len(req.Query) > 0 {
		u += "?" + req.Query.Encode()
	}
	return u, nil
}

func (c *Client) Do(ctx context.Context, req Request, out any) (*Response, error) {
	ep, err := c.registry.Lookup(req.Endpoint)
	if err != nil {
		return nil, err
	}
	if ep.Auth != c.auth {
		return nil, fmt.Errorf("endpoint %s membutuhkan token %s, bukan %s", ep.ID, ep.Auth, c.auth)
	}
	target, err := c.endpointURL(ep, req)
	if err != nil {
		return nil, err
	}
	var payload []byte
	if req.Body != nil {
		payload, err = json.Marshal(req.Body)
		if err != nil {
			return nil, fmt.Errorf("gagal menyusun body %s: %w", ep.ID, err)
		}
	}
	safe := ep.Method == http.MethodGet
	attempt := 0
	for {
		attempt++
		if err := c.limiter.Wait(ctx); err != nil {
			return nil, err
		}
		resp, sendErr := c.send(ctx, ep, target, payload)
		if sendErr != nil {
			if ctx.Err() != nil {
				return nil, ctx.Err()
			}
			retryable := safe || isDialError(sendErr)
			if !retryable {
				return nil, &APIError{Endpoint: ep.ID, Method: ep.Method, Kind: KindUncertain, Attempts: attempt, Cause: sendErr, Message: redactURL(sendErr.Error())}
			}
			if attempt > c.retryMax {
				return nil, &APIError{Endpoint: ep.ID, Method: ep.Method, Kind: KindNetwork, Attempts: attempt, Cause: errors.New(redactURL(sendErr.Error()))}
			}
			c.log.Warn("permintaan API gagal, mencoba ulang", "endpoint", ep.ID, "percobaan", attempt, "error", redactURL(sendErr.Error()))
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		}
		c.limiter.Observe(resp.Header)
		resp.Attempts = attempt
		switch {
		case resp.Status >= 200 && resp.Status < 300:
			resp.Accepted = resp.Status == http.StatusAccepted
			if out != nil && len(bytes.TrimSpace(resp.Body)) > 0 {
				if err := json.Unmarshal(resp.Body, out); err != nil {
					return resp, fmt.Errorf("respons %s tidak sesuai kontrak: %w", ep.ID, err)
				}
			}
			return resp, nil
		case resp.Status == http.StatusUnauthorized || resp.Status == http.StatusForbidden:
			return resp, c.apiError(ep, resp, KindUnauthorized, attempt)
		case resp.Status == http.StatusTooManyRequests:
			if attempt > c.retryMax {
				return resp, c.apiError(ep, resp, KindRateLimited, attempt)
			}
			wait, ok := parseRetryAfter(resp.Header.Get("Retry-After"), c.now())
			if !ok {
				wait = c.backoff(attempt)
			}
			c.limiter.PauseUntil(c.now().Add(wait))
			c.log.Warn("batas rate API tercapai, menunggu", "endpoint", ep.ID, "tunggu", wait.String())
			continue
		case resp.Status >= 500:
			if !safe {
				return resp, c.apiError(ep, resp, KindUncertain, attempt)
			}
			if attempt > c.retryMax || attempt > maxServerRetries {
				return resp, c.apiError(ep, resp, KindServer, attempt)
			}
			c.log.Warn("server Hostinger error, mencoba ulang", "endpoint", ep.ID, "status", resp.Status, "percobaan", attempt)
			if err := c.sleep(ctx, c.backoff(attempt)); err != nil {
				return nil, err
			}
			continue
		case resp.Status == http.StatusNotFound:
			return resp, c.apiError(ep, resp, KindNotFound, attempt)
		case resp.Status == http.StatusConflict:
			return resp, c.apiError(ep, resp, KindConflict, attempt)
		default:
			return resp, c.apiError(ep, resp, KindClient, attempt)
		}
	}
}

func (c *Client) send(ctx context.Context, ep Endpoint, target string, payload []byte) (*Response, error) {
	var body io.Reader
	if payload != nil {
		body = bytes.NewReader(payload)
	}
	httpReq, err := http.NewRequestWithContext(ctx, ep.Method, target, body)
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+c.token)
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	if payload != nil {
		httpReq.Header.Set("Content-Type", "application/json")
	}
	started := c.now()
	res, err := c.http.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	data, err := io.ReadAll(io.LimitReader(res.Body, maxBodyBytes))
	if err != nil {
		return nil, err
	}
	c.log.Debug("panggilan API", "endpoint", ep.ID, "method", ep.Method, "status", res.StatusCode, "durasi_ms", c.now().Sub(started).Milliseconds())
	return &Response{Status: res.StatusCode, Header: res.Header, Body: data}, nil
}

func (c *Client) apiError(ep Endpoint, resp *Response, kind ErrorKind, attempts int) error {
	return &APIError{
		Endpoint: ep.ID,
		Method:   ep.Method,
		Status:   resp.Status,
		Kind:     kind,
		Message:  extractMessage(resp.Body),
		Attempts: attempts,
	}
}

func (c *Client) backoff(attempt int) time.Duration {
	base := time.Second
	maxWait := 60 * time.Second
	d := time.Duration(float64(base) * math.Pow(2, float64(attempt-1)))
	if d > maxWait || d <= 0 {
		d = maxWait
	}
	factor := 0.5 + c.jitter()*0.5
	return time.Duration(float64(d) * factor)
}

func isDialError(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr)
}

var urlQuery = regexp.MustCompile(`\?[^\s"]*`)

func redactURL(s string) string {
	return urlQuery.ReplaceAllString(s, "?…")
}

type pageMeta struct {
	CurrentPage int `json:"current_page"`
	PerPage     int `json:"per_page"`
	Total       int `json:"total"`
}

type pageEnvelope[T any] struct {
	Data []T      `json:"data"`
	Meta pageMeta `json:"meta"`
}

func ListAll[T any](ctx context.Context, c *Client, req Request, perPage int) ([]T, error) {
	if perPage <= 0 || perPage > 100 {
		perPage = 100
	}
	var all []T
	for page := 1; page <= 10000; page++ {
		q := url.Values{}
		for k, v := range req.Query {
			q[k] = append([]string(nil), v...)
		}
		q.Set("page", fmt.Sprint(page))
		q.Set("per_page", fmt.Sprint(perPage))
		r := req
		r.Query = q
		var env pageEnvelope[T]
		if _, err := c.Do(ctx, r, &env); err != nil {
			return nil, err
		}
		all = append(all, env.Data...)
		if len(env.Data) == 0 {
			break
		}
		if env.Meta.Total > 0 && len(all) >= env.Meta.Total {
			break
		}
		if env.Meta.Total == 0 && len(env.Data) < perPage {
			break
		}
	}
	return all, nil
}
