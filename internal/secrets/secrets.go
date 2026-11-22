package secrets

import (
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/zalando/go-keyring"

	"hostinger-migrator/internal/logx"
)

const Service = "hostinger-migrator"

var ErrNotFound = errors.New("rahasia tidak ditemukan di Credential Manager")

var aliasPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,31}$`)

func ValidateAlias(alias string) error {
	if !aliasPattern.MatchString(alias) {
		return fmt.Errorf("alias %q tidak valid: gunakan 1-32 karakter huruf kecil, angka, '-' atau '_', diawali huruf/angka", alias)
	}
	return nil
}

func ValidateToken(token string) error {
	if strings.TrimSpace(token) != token || token == "" {
		return errors.New("token kosong atau mengandung spasi di awal/akhir")
	}
	if len(token) < 16 {
		return errors.New("token terlalu pendek, periksa kembali token yang disalin dari hPanel")
	}
	if strings.ContainsAny(token, " \t\r\n") {
		return errors.New("token tidak boleh mengandung spasi atau baris baru")
	}
	return nil
}

type Backend interface {
	Set(service, user, secret string) error
	Get(service, user string) (string, error)
	Delete(service, user string) error
}

type keyringBackend struct{}

func (keyringBackend) Set(service, user, secret string) error {
	return keyring.Set(service, user, secret)
}
func (keyringBackend) Get(service, user string) (string, error) {
	return keyring.Get(service, user)
}
func (keyringBackend) Delete(service, user string) error { return keyring.Delete(service, user) }

type Store struct {
	backend  Backend
	redactor *logx.Redactor
}

func NewKeyring(r *logx.Redactor) *Store {
	return New(keyringBackend{}, r)
}

func New(b Backend, r *logx.Redactor) *Store {
	if r == nil {
		r = logx.Global
	}
	return &Store{backend: b, redactor: r}
}

func tokenKey(alias string) string {
	return alias
}

func (s *Store) SetToken(alias, token string) error {
	if err := ValidateAlias(alias); err != nil {
		return err
	}
	if err := ValidateToken(token); err != nil {
		return err
	}
	s.redactor.Add(token)
	if err := s.backend.Set(Service, tokenKey(alias), token); err != nil {
		return fmt.Errorf("gagal menyimpan token ke Credential Manager: %s", s.redactor.String(err.Error()))
	}
	return nil
}

func (s *Store) Token(alias string) (string, error) {
	v, err := s.backend.Get(Service, tokenKey(alias))
	if errors.Is(err, keyring.ErrNotFound) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", fmt.Errorf("gagal membaca token dari Credential Manager: %s", s.redactor.String(err.Error()))
	}
	s.redactor.Add(v)
	return v, nil
}

func (s *Store) HasToken(alias string) (bool, error) {
	_, err := s.Token(alias)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

func (s *Store) DeleteToken(alias string) error {
	err := s.backend.Delete(Service, tokenKey(alias))
	if errors.Is(err, keyring.ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("gagal menghapus token dari Credential Manager: %w", err)
	}
	return nil
}

type MemoryBackend struct {
	data map[string]string
	Fail error
}

func NewMemoryBackend() *MemoryBackend {
	return &MemoryBackend{data: map[string]string{}}
}

func (m *MemoryBackend) Set(service, user, secret string) error {
	if m.Fail != nil {
		return m.Fail
	}
	m.data[service+"\x00"+user] = secret
	return nil
}

func (m *MemoryBackend) Get(service, user string) (string, error) {
	v, ok := m.data[service+"\x00"+user]
	if !ok {
		return "", keyring.ErrNotFound
	}
	return v, nil
}

func (m *MemoryBackend) Delete(service, user string) error {
	k := service + "\x00" + user
	if _, ok := m.data[k]; !ok {
		return keyring.ErrNotFound
	}
	delete(m.data, k)
	return nil
}

func (m *MemoryBackend) Len() int {
	return len(m.data)
}
