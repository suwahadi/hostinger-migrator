package account

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"

	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/logx"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

var (
	ErrAliasExists   = errors.New("alias sudah terdaftar")
	ErrAliasNotFound = errors.New("alias tidak terdaftar")
	ErrTokenRejected = errors.New("token ditolak Hostinger")
	ErrTokenMissing  = errors.New("token tidak ditemukan di Credential Manager")
)

type ClientFactory func(alias, token string) (*hostinger.Client, error)

type Manager struct {
	Store     *store.Store
	Secrets   *secrets.Store
	NewClient ClientFactory
	Log       *slog.Logger
	Redactor  *logx.Redactor
}

func (m *Manager) redactor() *logx.Redactor {
	if m.Redactor == nil {
		return logx.Global
	}
	return m.Redactor
}

func (m *Manager) redact(s string) string {
	return m.redactor().String(s)
}

type Result struct {
	Alias     string
	Status    string
	Discovery *hostinger.Discovery
}

func (m *Manager) Add(ctx context.Context, alias, token string) (*Result, error) {
	if err := secrets.ValidateAlias(alias); err != nil {
		return nil, err
	}
	if err := secrets.ValidateToken(token); err != nil {
		return nil, err
	}
	m.redactor().Add(token)
	exists, err := m.Store.CredentialExists(ctx, alias)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, fmt.Errorf("%w: %s", ErrAliasExists, alias)
	}
	inKeyring, err := m.Secrets.HasToken(alias)
	if err != nil {
		return nil, err
	}
	if inKeyring {
		return nil, fmt.Errorf("%w di Credential Manager: %s (hapus dulu dengan 'migrator account remove %s')", ErrAliasExists, alias, alias)
	}
	client, err := m.NewClient(alias, token)
	if err != nil {
		return nil, err
	}
	disc, err := hostinger.Discover(ctx, client)
	if err != nil {
		if hostinger.IsBlocking(err) {
			return nil, fmt.Errorf("%w: %v", ErrTokenRejected, err)
		}
		return nil, fmt.Errorf("validasi token gagal: %w", err)
	}
	if err := m.Secrets.SetToken(alias, token); err != nil {
		return nil, err
	}
	err = m.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := m.Store.InsertCredential(ctx, tx, alias); err != nil {
			return err
		}
		if err := m.Store.ReplaceHostingAccounts(ctx, tx, alias, toStore(disc)); err != nil {
			return err
		}
		return m.Store.AppendAudit(ctx, tx, "operator", "account.add", alias, "OK", fmt.Sprintf("%d akun hosting ditemukan", len(disc.Accounts)))
	})
	if err != nil {
		if delErr := m.Secrets.DeleteToken(alias); delErr != nil {
			m.Log.Error("gagal membatalkan penyimpanan token", "alias", alias, "error", delErr)
		}
		return nil, err
	}
	m.Log.Info("token ditambahkan", "alias", alias, "akun_hosting", len(disc.Accounts))
	return &Result{Alias: alias, Status: store.CredentialActive, Discovery: disc}, nil
}

func (m *Manager) Test(ctx context.Context, alias string) (*Result, error) {
	if _, err := m.Store.GetCredential(ctx, alias); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, fmt.Errorf("%w: %s", ErrAliasNotFound, alias)
		}
		return nil, err
	}
	token, err := m.Secrets.Token(alias)
	if errors.Is(err, secrets.ErrNotFound) {
		m.recordStatus(ctx, alias, store.CredentialError, "token tidak ada di Credential Manager")
		return &Result{Alias: alias, Status: store.CredentialError}, fmt.Errorf("%w untuk alias %s; hapus lalu daftarkan ulang", ErrTokenMissing, alias)
	}
	if err != nil {
		return nil, err
	}
	client, err := m.NewClient(alias, token)
	if err != nil {
		return nil, err
	}
	disc, err := hostinger.Discover(ctx, client)
	if err != nil {
		status := store.CredentialError
		if hostinger.IsBlocking(err) {
			status = store.CredentialRejected
		}
		m.recordStatus(ctx, alias, status, err.Error())
		if status == store.CredentialRejected {
			return &Result{Alias: alias, Status: status}, fmt.Errorf("%w: %v", ErrTokenRejected, err)
		}
		return &Result{Alias: alias, Status: status}, err
	}
	err = m.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if err := m.Store.UpdateCredentialStatus(ctx, tx, alias, store.CredentialActive, ""); err != nil {
			return err
		}
		if err := m.Store.ReplaceHostingAccounts(ctx, tx, alias, toStore(disc)); err != nil {
			return err
		}
		return m.Store.AppendAudit(ctx, tx, "operator", "account.test", alias, store.CredentialActive, fmt.Sprintf("%d akun hosting ditemukan", len(disc.Accounts)))
	})
	if err != nil {
		return nil, err
	}
	return &Result{Alias: alias, Status: store.CredentialActive, Discovery: disc}, nil
}

func (m *Manager) recordStatus(ctx context.Context, alias, status, msg string) {
	err := m.Store.WithTx(ctx, func(tx *sql.Tx) error {
		clean := m.redact(msg)
		if err := m.Store.UpdateCredentialStatus(ctx, tx, alias, status, clean); err != nil {
			return err
		}
		return m.Store.AppendAudit(ctx, tx, "operator", "account.test", alias, status, clean)
	})
	if err != nil {
		m.Log.Error("gagal mencatat status token", "alias", alias, "error", err)
	}
}

func (m *Manager) Remove(ctx context.Context, alias string) error {
	inDB, err := m.Store.CredentialExists(ctx, alias)
	if err != nil {
		return err
	}
	keyErr := m.Secrets.DeleteToken(alias)
	inKeyring := keyErr == nil
	if keyErr != nil && !errors.Is(keyErr, secrets.ErrNotFound) {
		return keyErr
	}
	if !inDB && !inKeyring {
		return fmt.Errorf("%w: %s", ErrAliasNotFound, alias)
	}
	return m.Store.WithTx(ctx, func(tx *sql.Tx) error {
		if inDB {
			if err := m.Store.DeleteCredential(ctx, tx, alias); err != nil {
				return err
			}
		}
		return m.Store.AppendAudit(ctx, tx, "operator", "account.remove", alias, "OK", "credential lokal dihapus, Hostinger tidak disentuh")
	})
}

type Listing struct {
	Credential store.Credential
	Accounts   []store.HostingAccount
}

func (m *Manager) List(ctx context.Context) ([]Listing, error) {
	creds, err := m.Store.ListCredentials(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Listing, 0, len(creds))
	for _, c := range creds {
		accs, err := m.Store.ListHostingAccounts(ctx, c.Alias)
		if err != nil {
			return nil, err
		}
		out = append(out, Listing{Credential: c, Accounts: accs})
	}
	return out, nil
}

func toStore(d *hostinger.Discovery) []store.HostingAccount {
	out := make([]store.HostingAccount, 0, len(d.Accounts))
	for _, a := range d.Accounts {
		out = append(out, store.HostingAccount{
			ID:           a.Username,
			OrderID:      a.OrderID,
			Plan:         a.Plan,
			OrderStatus:  a.OrderStatus,
			WebsiteCount: a.WebsiteCount,
		})
	}
	return out
}
