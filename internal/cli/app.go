package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/config"
	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/email"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

var Version = "dev"

const (
	ExitOK     = 0
	ExitError  = 1
	ExitAction = 2
)

type Env struct {
	Stdin          io.Reader
	Stdout         io.Writer
	Stderr         io.Writer
	ReadSecret     func() (string, error)
	SecretsBackend secrets.Backend
	HTTPClient     *http.Client
	Redactor       *logx.Redactor
	Now            func() time.Time
	LookupNS       func(ctx context.Context, domain string) ([]string, error)
	SpawnWorker    func(ctx context.Context, a *App) error
	RunnerHook     func(d *pipeline.Deps)
	StageHook      func(s *cutover.Stage)
	EmailHook      func(s *email.Stage)
	Resolve        func(ctx context.Context, host string) ([]string, error)
}

type App struct {
	env        Env
	configPath string
	dataDir    string
	cfg        *config.Config
	store      *store.Store
	secrets    *secrets.Store
	log        *slog.Logger
	closeLog   func() error
	limiters   *hostinger.LimiterPool
	prompt     *Prompter
	redactor   *logx.Redactor
	lookupNS   func(ctx context.Context, domain string) ([]string, error)
	wizardMode bool
}

type exitError struct {
	code int
	err  error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

func needsAction(err error) error {
	return &exitError{code: ExitAction, err: err}
}

func newApp(env Env) *App {
	if env.Stdin == nil {
		env.Stdin = os.Stdin
	}
	if env.Stdout == nil {
		env.Stdout = os.Stdout
	}
	if env.Stderr == nil {
		env.Stderr = os.Stderr
	}
	if env.Redactor == nil {
		env.Redactor = logx.Global
	}
	return &App{
		env:      env,
		redactor: env.Redactor,
		prompt:   NewPrompter(env.Stdin, env.Stdout, env.Stderr, env.ReadSecret),
		lookupNS: env.LookupNS,
	}
}

func (a *App) out() io.Writer { return a.env.Stdout }

func (a *App) printf(format string, args ...any) {
	fmt.Fprintf(a.env.Stdout, format, args...)
}

func (a *App) println(args ...any) {
	fmt.Fprintln(a.env.Stdout, args...)
}

func (a *App) open(ctx context.Context) error {
	if a.store != nil {
		return nil
	}
	if _, err := os.Stat(a.configPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(a.configPath, config.DefaultYAML(), 0o600); err != nil {
			return fmt.Errorf("gagal membuat config bawaan %s: %w", a.configPath, err)
		}
	}
	cfg, err := config.Load(a.configPath)
	if err != nil {
		return err
	}
	a.cfg = cfg
	a.dataDir = cfg.DataDir(a.configPath)
	if err := os.MkdirAll(a.dataDir, 0o700); err != nil {
		return fmt.Errorf("gagal membuat folder data %s: %w", a.dataDir, err)
	}
	if warn := config.CloudSyncWarning(a.dataDir); warn != "" {
		fmt.Fprintln(a.env.Stderr, warn)
	}
	logger, closeLog, err := logx.New(logx.Options{
		Dir:           filepath.Join(a.dataDir, "logs"),
		Terminal:      a.env.Stderr,
		TerminalLevel: slog.LevelWarn,
		FileLevel:     slog.LevelDebug,
		Redactor:      a.redactor,
		Now:           a.env.Now,
	})
	if err != nil {
		return err
	}
	a.log = logger
	a.closeLog = closeLog
	st, err := store.Open(ctx, filepath.Join(a.dataDir, "migrator.db"))
	if err != nil {
		closeLog()
		return err
	}
	a.store = st
	if a.env.SecretsBackend != nil {
		a.secrets = secrets.New(a.env.SecretsBackend, a.redactor)
	} else {
		a.secrets = secrets.NewKeyring(a.redactor)
	}
	a.limiters = hostinger.NewLimiterPool(cfg.API.RatePerMenit)
	a.log.Debug("migrator dibuka", "versi", Version, "data_dir", a.dataDir)
	return nil
}

func (a *App) close() {
	if a.store != nil {
		a.store.Close()
		a.store = nil
	}
	if a.closeLog != nil {
		a.closeLog()
		a.closeLog = nil
	}
}

func (a *App) newClient(alias, token string) (*hostinger.Client, error) {
	return hostinger.NewClient(hostinger.Options{
		Token:      token,
		BaseURL:    a.cfg.API.BaseURL,
		Timeout:    time.Duration(a.cfg.API.TimeoutDetik) * time.Second,
		RetryMax:   a.cfg.API.RetryMaks,
		Limiter:    a.limiters.For(alias),
		HTTPClient: a.env.HTTPClient,
		Logger:     a.log.With("alias", alias),
		UserAgent:  "hostinger-migrator/" + Version,
	})
}

func (a *App) accounts() *account.Manager {
	return &account.Manager{Store: a.store, Secrets: a.secrets, NewClient: a.newClient, Log: a.log, Redactor: a.redactor}
}
