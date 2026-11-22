package logx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

type Options struct {
	Dir           string
	Terminal      io.Writer
	TerminalLevel slog.Level
	FileLevel     slog.Level
	Redactor      *Redactor
	Now           func() time.Time
}

func New(opts Options) (*slog.Logger, func() error, error) {
	r := opts.Redactor
	if r == nil {
		r = Global
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	var handlers []slog.Handler
	closeFn := func() error { return nil }
	if opts.Dir != "" {
		if err := os.MkdirAll(opts.Dir, 0o700); err != nil {
			return nil, nil, fmt.Errorf("gagal membuat folder log: %w", err)
		}
		name := filepath.Join(opts.Dir, "migrator-"+now().Format("20060102")+".log")
		f, err := os.OpenFile(name, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return nil, nil, fmt.Errorf("gagal membuka file log: %w", err)
		}
		handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: opts.FileLevel}))
		closeFn = f.Close
	}
	if opts.Terminal != nil {
		handlers = append(handlers, slog.NewTextHandler(opts.Terminal, &slog.HandlerOptions{Level: opts.TerminalLevel}))
	}
	var h slog.Handler
	switch len(handlers) {
	case 0:
		h = slog.NewTextHandler(io.Discard, nil)
	case 1:
		h = handlers[0]
	default:
		h = fanout(handlers)
	}
	return slog.New(&redactHandler{inner: h, r: r}), closeFn, nil
}

type redactHandler struct {
	inner slog.Handler
	r     *Redactor
}

func NewRedactingHandler(inner slog.Handler, r *Redactor) slog.Handler {
	if r == nil {
		r = Global
	}
	return &redactHandler{inner: inner, r: r}
}

func (h *redactHandler) Enabled(ctx context.Context, l slog.Level) bool {
	return h.inner.Enabled(ctx, l)
}

func (h *redactHandler) Handle(ctx context.Context, rec slog.Record) error {
	out := slog.NewRecord(rec.Time, rec.Level, h.r.String(rec.Message), rec.PC)
	rec.Attrs(func(a slog.Attr) bool {
		out.AddAttrs(h.attr(a))
		return true
	})
	return h.inner.Handle(ctx, out)
}

func (h *redactHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clean := make([]slog.Attr, 0, len(attrs))
	for _, a := range attrs {
		clean = append(clean, h.attr(a))
	}
	return &redactHandler{inner: h.inner.WithAttrs(clean), r: h.r}
}

func (h *redactHandler) WithGroup(name string) slog.Handler {
	return &redactHandler{inner: h.inner.WithGroup(name), r: h.r}
}

func (h *redactHandler) attr(a slog.Attr) slog.Attr {
	v := a.Value.Resolve()
	if v.Kind() == slog.KindGroup {
		group := v.Group()
		clean := make([]any, 0, len(group))
		for _, g := range group {
			clean = append(clean, h.attr(g))
		}
		return slog.Group(a.Key, clean...)
	}
	if IsSensitiveKey(a.Key) {
		return slog.String(a.Key, Mask)
	}
	switch v.Kind() {
	case slog.KindString:
		return slog.String(a.Key, h.r.String(v.String()))
	case slog.KindAny:
		switch x := v.Any().(type) {
		case error:
			return slog.String(a.Key, h.r.String(x.Error()))
		case fmt.Stringer:
			return slog.String(a.Key, h.r.String(x.String()))
		case []byte:
			return slog.String(a.Key, h.r.String(string(x)))
		default:
			return slog.String(a.Key, h.r.String(fmt.Sprintf("%+v", x)))
		}
	default:
		return slog.Attr{Key: a.Key, Value: v}
	}
}

type fanout []slog.Handler

func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			if err := h.Handle(ctx, r.Clone()); err != nil {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}
