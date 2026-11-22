package sshx

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

type Service struct {
	Store   *store.Store
	Secrets *secrets.Store
	KeyDir  string
	Timeout time.Duration
	Log     *slog.Logger
	keys    *KeyPair
}

type CheckResult struct {
	Info        store.SSHInfo
	Whoami      string
	Home        string
	SFTPDir     string
	Cause       string
	Message     string
	NewHostKey  bool
	Fingerprint string
}

func (r *CheckResult) Ready() bool {
	return r.Info.Status == store.SSHReady
}

var ErrBlocked = errors.New("akun SSH berstatus BLOCKED karena fingerprint berubah")

func (s *Service) Keys() (*KeyPair, error) {
	if s.keys != nil {
		return s.keys, nil
	}
	kp, err := EnsureKeyPair(s.KeyDir)
	if err != nil {
		return nil, err
	}
	s.keys = kp
	return kp, nil
}

func (s *Service) Check(ctx context.Context, info store.SSHInfo, acceptNewHost bool) (*CheckResult, error) {
	kp, err := s.Keys()
	if err != nil {
		return nil, err
	}
	target := Target{Host: info.Host, Port: info.Port, User: info.User}
	if err := target.Validate(); err != nil {
		return nil, err
	}
	expected := info.Fingerprint
	if acceptNewHost {
		expected = ""
	}
	password, pwErr := s.Secrets.SSHPassword(info.AccountID)
	if pwErr != nil && !errors.Is(pwErr, secrets.ErrNotFound) {
		return nil, pwErr
	}
	res := &CheckResult{Info: info}
	client, authUsed, err := s.dial(ctx, target, kp, password, expected)
	if err != nil {
		res.Cause, res.Message = Classify(err)
		res.Info.LastError = res.Message
		if res.Cause == CauseHostKeyChanged {
			res.Info.Status = store.SSHBlocked
		} else {
			res.Info.Status = store.SSHFailed
		}
		if saveErr := s.save(ctx, res, "GAGAL"); saveErr != nil {
			return nil, saveErr
		}
		return res, nil
	}
	defer client.Close()
	res.Fingerprint = client.Fingerprint
	res.NewHostKey = info.Fingerprint == "" || info.Fingerprint != client.Fingerprint
	out, err := client.Run(ctx, "echo ok; whoami; pwd", nil)
	if err != nil || out.ExitCode != 0 {
		res.Cause = CauseOther
		res.Message = "Login SSH berhasil, tetapi perintah uji gagal dijalankan di server."
		if err != nil {
			res.Message += " " + err.Error()
		}
		res.Info.Status = store.SSHFailed
		res.Info.LastError = res.Message
		return res, s.save(ctx, res, "GAGAL")
	}
	lines := strings.Split(strings.TrimSpace(out.Stdout), "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "ok" {
		res.Cause = CauseOther
		res.Message = "Respons perintah uji tidak sesuai harapan (" + strings.TrimSpace(out.Stdout) + ")."
		res.Info.Status = store.SSHFailed
		res.Info.LastError = res.Message
		return res, s.save(ctx, res, "GAGAL")
	}
	res.Whoami = strings.TrimSpace(lines[1])
	res.Home = strings.TrimSpace(lines[2])
	if res.Whoami != info.User {
		res.Cause = CauseOther
		res.Message = fmt.Sprintf("Login sebagai %q, tetapi whoami mengembalikan %q.", info.User, res.Whoami)
		res.Info.Status = store.SSHFailed
		res.Info.LastError = res.Message
		return res, s.save(ctx, res, "GAGAL")
	}
	dir, err := client.CheckSFTP()
	if err != nil {
		res.Cause = CauseOther
		res.Message = "SSH berfungsi tetapi SFTP gagal: " + err.Error()
		res.Info.Status = store.SSHFailed
		res.Info.LastError = res.Message
		return res, s.save(ctx, res, "GAGAL")
	}
	res.SFTPDir = dir
	res.Info.Status = store.SSHReady
	res.Info.Fingerprint = client.Fingerprint
	res.Info.Auth = authUsed
	res.Info.Home = res.Home
	res.Info.LastError = ""
	return res, s.save(ctx, res, store.SSHReady)
}

func (s *Service) dial(ctx context.Context, target Target, kp *KeyPair, password, expected string) (*Client, string, error) {
	c, err := Dial(ctx, DialOptions{Target: target, Auth: Auth{Signer: kp.Signer}, ExpectedFingerprint: expected, Timeout: s.Timeout})
	if err == nil {
		return c, store.SSHAuthKey, nil
	}
	if password == "" {
		return nil, "", err
	}
	if cause, _ := Classify(err); cause != CauseAuth {
		return nil, "", err
	}
	c, err = Dial(ctx, DialOptions{Target: target, Auth: Auth{Password: password}, ExpectedFingerprint: expected, Timeout: s.Timeout})
	if err != nil {
		return nil, "", err
	}
	return c, store.SSHAuthPassword, nil
}

func (s *Service) save(ctx context.Context, res *CheckResult, result string) error {
	if err := s.Store.SaveSSH(ctx, res.Info); err != nil {
		return err
	}
	detail := fmt.Sprintf("%s:%d user %s", res.Info.Host, res.Info.Port, res.Info.User)
	if res.Message != "" {
		detail += " — " + res.Message
	}
	if res.Fingerprint != "" {
		detail += " — fingerprint " + res.Fingerprint
	}
	if s.Log != nil {
		s.Log.Info("tes SSH", "akun", res.Info.AccountID, "status", res.Info.Status, "detail", detail)
	}
	return s.Store.AppendAudit(ctx, nil, "operator", "ssh.test", res.Info.AccountID, result, detail)
}

func (s *Service) Connect(ctx context.Context, accountID string) (*Client, error) {
	info, err := s.Store.GetSSH(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if info.Status == store.SSHBlocked {
		return nil, fmt.Errorf("%w: %s", ErrBlocked, accountID)
	}
	if info.Status != store.SSHReady {
		return nil, fmt.Errorf("SSH akun %s belum siap (status %s); jalankan 'migrator ssh setup %s'", accountID, info.Status, accountID)
	}
	kp, err := s.Keys()
	if err != nil {
		return nil, err
	}
	password, pwErr := s.Secrets.SSHPassword(accountID)
	if pwErr != nil && !errors.Is(pwErr, secrets.ErrNotFound) {
		return nil, pwErr
	}
	c, _, err := s.dial(ctx, Target{Host: info.Host, Port: info.Port, User: info.User}, kp, password, info.Fingerprint)
	if err != nil {
		var hk *HostKeyChangedError
		if errors.As(err, &hk) {
			info.Status = store.SSHBlocked
			_, msg := Classify(err)
			info.LastError = msg
			s.Store.SaveSSH(ctx, *info)
			s.Store.AppendAudit(ctx, nil, "worker", "ssh.connect", accountID, store.SSHBlocked, msg)
			return nil, fmt.Errorf("%w: %s", ErrBlocked, accountID)
		}
		return nil, err
	}
	return c, nil
}
