package hostinger

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Limiter struct {
	mu          sync.Mutex
	interval    time.Duration
	burst       int
	next        time.Time
	pausedUntil time.Time
	now         func() time.Time
	sleep       func(context.Context, time.Duration) error
}

func NewLimiter(perMinute int) *Limiter {
	return NewLimiterWithBurst(perMinute, 1)
}

func NewLimiterWithBurst(perMinute, burst int) *Limiter {
	if perMinute < 1 {
		perMinute = 1
	}
	if burst < 1 {
		burst = 1
	}
	return &Limiter{
		interval: time.Minute / time.Duration(perMinute),
		burst:    burst,
		now:      time.Now,
		sleep:    sleepCtx,
	}
}

func (l *Limiter) SetClock(now func() time.Time, sleep func(context.Context, time.Duration) error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now != nil {
		l.now = now
	}
	if sleep != nil {
		l.sleep = sleep
	}
}

func (l *Limiter) Wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	slot := l.next
	if floor := now.Add(-time.Duration(l.burst-1) * l.interval); slot.Before(floor) {
		slot = floor
	}
	if l.pausedUntil.After(slot) {
		slot = l.pausedUntil
	}
	l.next = slot.Add(l.interval)
	sleep := l.sleep
	l.mu.Unlock()
	if d := slot.Sub(now); d > 0 {
		return sleep(ctx, d)
	}
	return ctx.Err()
}

func (l *Limiter) PauseUntil(t time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if t.After(l.pausedUntil) {
		l.pausedUntil = t
	}
}

func (l *Limiter) Observe(h http.Header) {
	if remaining, reset, ok := parseIETFRateLimit(h.Get("RateLimit")); ok {
		if remaining <= 0 {
			wait := time.Duration(reset) * time.Second
			if wait <= 0 {
				wait = time.Second
			}
			l.PauseUntil(l.now().Add(wait))
		}
		return
	}
	remaining := strings.TrimSpace(h.Get("X-RateLimit-Remaining"))
	if remaining != "0" {
		return
	}
	reset := strings.TrimSpace(h.Get("X-RateLimit-Reset"))
	if reset == "" {
		l.PauseUntil(l.now().Add(time.Minute))
		return
	}
	v, err := strconv.ParseInt(reset, 10, 64)
	if err != nil || v <= 0 {
		return
	}
	now := l.now()
	if v > 1_000_000_000 {
		l.PauseUntil(time.Unix(v, 0))
		return
	}
	l.PauseUntil(now.Add(time.Duration(v) * time.Second))
}

func parseIETFRateLimit(v string) (remaining, reset int, ok bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, 0, false
	}
	first := strings.Split(v, ",")[0]
	var hasR bool
	for _, part := range strings.Split(first, ";") {
		kv := strings.SplitN(strings.TrimSpace(part), "=", 2)
		if len(kv) != 2 {
			continue
		}
		n, err := strconv.Atoi(strings.Trim(kv[1], `"`))
		if err != nil {
			continue
		}
		switch kv[0] {
		case "r":
			remaining = n
			hasR = true
		case "t":
			reset = n
		}
	}
	return remaining, reset, hasR
}

func (l *Limiter) Interval() time.Duration {
	return l.interval
}

type LimiterPool struct {
	mu        sync.Mutex
	perMinute int
	burst     int
	byKey     map[string]*Limiter
}

func NewLimiterPool(perMinute int) *LimiterPool {
	return NewLimiterPoolWithBurst(perMinute, 1)
}

func NewLimiterPoolWithBurst(perMinute, burst int) *LimiterPool {
	return &LimiterPool{perMinute: perMinute, burst: burst, byKey: map[string]*Limiter{}}
}

func (p *LimiterPool) For(key string) *Limiter {
	p.mu.Lock()
	defer p.mu.Unlock()
	l, ok := p.byKey[key]
	if !ok {
		l = NewLimiterWithBurst(p.perMinute, p.burst)
		p.byKey[key] = l
	}
	return l
}

func parseRetryAfter(v string, now time.Time) (time.Duration, bool) {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0, false
	}
	if secs, err := strconv.Atoi(v); err == nil {
		if secs < 0 {
			return 0, false
		}
		return time.Duration(secs) * time.Second, true
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d < 0 {
			d = 0
		}
		return d, true
	}
	return 0, false
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
