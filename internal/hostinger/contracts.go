package hostinger

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

//go:embed contracts/*.yaml
var contractFiles embed.FS

const (
	AuthHPanel  = "bearer_token_hpanel"
	AuthMailAPI = "bearer_token_mail_api"

	StatusAvailable  = "tersedia"
	StatusDeprecated = "deprecated"
)

var (
	ErrUnknownEndpoint    = errors.New("endpoint tidak terdaftar di contract registry")
	ErrEndpointDeprecated = errors.New("endpoint berstatus deprecated dan tidak boleh dipakai")
)

type Endpoint struct {
	ID          string `yaml:"id"`
	OperationID string `yaml:"operation_id"`
	Method      string `yaml:"method"`
	Path        string `yaml:"path"`
	Status      string `yaml:"status_spesifikasi"`
	Destructive bool   `yaml:"destruktif"`
	Async       bool   `yaml:"asinkron"`
	Paginated   bool   `yaml:"paginasi"`
	BaseURL     string `yaml:"-"`
	Auth        string `yaml:"-"`
	Source      string `yaml:"-"`
	SpecVersion string `yaml:"-"`
}

type contractFile struct {
	Sumber struct {
		BaseURL     string `yaml:"base_url"`
		Autentikasi string `yaml:"autentikasi"`
		Versi       string `yaml:"versi_spesifikasi"`
	} `yaml:"sumber"`
	Endpoint []Endpoint `yaml:"endpoint"`
}

type Registry struct {
	byID map[string]Endpoint
}

var (
	defaultRegistry     *Registry
	defaultRegistryErr  error
	defaultRegistryOnce sync.Once
)

func DefaultRegistry() (*Registry, error) {
	defaultRegistryOnce.Do(func() {
		defaultRegistry, defaultRegistryErr = LoadRegistry(contractFiles)
	})
	return defaultRegistry, defaultRegistryErr
}

func LoadRegistry(fsys fs.FS) (*Registry, error) {
	names, err := fs.Glob(fsys, "contracts/*.yaml")
	if err != nil {
		return nil, err
	}
	if len(names) == 0 {
		return nil, errors.New("tidak ada file kontrak API")
	}
	sort.Strings(names)
	reg := &Registry{byID: map[string]Endpoint{}}
	for _, name := range names {
		raw, err := fs.ReadFile(fsys, name)
		if err != nil {
			return nil, err
		}
		var cf contractFile
		if err := yaml.Unmarshal(raw, &cf); err != nil {
			return nil, fmt.Errorf("kontrak %s tidak valid: %w", name, err)
		}
		if !strings.HasPrefix(cf.Sumber.BaseURL, "https://") {
			return nil, fmt.Errorf("kontrak %s: base_url wajib https", name)
		}
		if cf.Sumber.Autentikasi != AuthHPanel && cf.Sumber.Autentikasi != AuthMailAPI {
			return nil, fmt.Errorf("kontrak %s: autentikasi %q tidak dikenal", name, cf.Sumber.Autentikasi)
		}
		for _, ep := range cf.Endpoint {
			if err := validateEndpoint(ep); err != nil {
				return nil, fmt.Errorf("kontrak %s: %w", name, err)
			}
			if _, dup := reg.byID[ep.ID]; dup {
				return nil, fmt.Errorf("kontrak %s: id %s ganda", name, ep.ID)
			}
			ep.Method = strings.ToUpper(ep.Method)
			ep.BaseURL = strings.TrimRight(cf.Sumber.BaseURL, "/")
			ep.Auth = cf.Sumber.Autentikasi
			ep.Source = name
			ep.SpecVersion = cf.Sumber.Versi
			reg.byID[ep.ID] = ep
		}
	}
	return reg, nil
}

func validateEndpoint(ep Endpoint) error {
	if ep.ID == "" || ep.OperationID == "" || ep.Path == "" {
		return fmt.Errorf("endpoint %q tidak lengkap", ep.ID)
	}
	if strings.Contains(ep.Path, "agency-hosting") {
		return fmt.Errorf("endpoint %s memakai namespace agency-hosting yang dilarang untuk Shared Hosting", ep.ID)
	}
	if !strings.HasPrefix(ep.Path, "/api/") {
		return fmt.Errorf("endpoint %s: path harus diawali /api/", ep.ID)
	}
	switch strings.ToUpper(ep.Method) {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
	default:
		return fmt.Errorf("endpoint %s: method %q tidak valid", ep.ID, ep.Method)
	}
	switch ep.Status {
	case StatusAvailable, StatusDeprecated:
	default:
		return fmt.Errorf("endpoint %s: status_spesifikasi %q tidak dikenal", ep.ID, ep.Status)
	}
	return nil
}

func (r *Registry) Lookup(id string) (Endpoint, error) {
	ep, ok := r.byID[id]
	if !ok {
		return Endpoint{}, fmt.Errorf("%w: %s", ErrUnknownEndpoint, id)
	}
	if ep.Status == StatusDeprecated {
		return Endpoint{}, fmt.Errorf("%w: %s", ErrEndpointDeprecated, id)
	}
	return ep, nil
}

func (r *Registry) IDs() []string {
	out := make([]string, 0, len(r.byID))
	for id := range r.byID {
		out = append(out, id)
	}
	sort.Strings(out)
	return out
}
