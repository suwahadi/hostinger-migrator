package sshx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Target struct {
	Host string
	Port int
	User string
}

func (t Target) Addr() string {
	return net.JoinHostPort(t.Host, strconv.Itoa(t.Port))
}

func (t Target) Validate() error {
	if strings.TrimSpace(t.Host) == "" {
		return errors.New("host SSH kosong")
	}
	if t.Port < 1 || t.Port > 65535 {
		return fmt.Errorf("port SSH %d tidak valid", t.Port)
	}
	if strings.TrimSpace(t.User) == "" {
		return errors.New("username SSH kosong")
	}
	return nil
}

type Auth struct {
	Signer   ssh.Signer
	Password string
}

type HostKeyChangedError struct {
	Expected string
	Got      string
}

func (e *HostKeyChangedError) Error() string {
	return fmt.Sprintf("fingerprint host SSH berubah (tersimpan %s, sekarang %s)", e.Expected, e.Got)
}

type Client struct {
	conn        *ssh.Client
	Fingerprint string
}

type DialOptions struct {
	Target              Target
	Auth                Auth
	ExpectedFingerprint string
	Timeout             time.Duration
}

func Dial(ctx context.Context, o DialOptions) (*Client, error) {
	if err := o.Target.Validate(); err != nil {
		return nil, err
	}
	var methods []ssh.AuthMethod
	if o.Auth.Signer != nil {
		methods = append(methods, ssh.PublicKeys(o.Auth.Signer))
	}
	if o.Auth.Password != "" {
		pw := o.Auth.Password
		methods = append(methods, ssh.Password(pw), ssh.KeyboardInteractive(func(name, instruction string, questions []string, echos []bool) ([]string, error) {
			answers := make([]string, len(questions))
			for i := range questions {
				answers[i] = pw
			}
			return answers, nil
		}))
	}
	if len(methods) == 0 {
		return nil, errors.New("tidak ada metode autentikasi SSH")
	}
	timeout := o.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	var seen string
	cfg := &ssh.ClientConfig{
		User: o.Target.User,
		Auth: methods,
		HostKeyCallback: func(hostname string, remote net.Addr, key ssh.PublicKey) error {
			seen = Fingerprint(key)
			if o.ExpectedFingerprint != "" && o.ExpectedFingerprint != seen {
				return &HostKeyChangedError{Expected: o.ExpectedFingerprint, Got: seen}
			}
			return nil
		},
		Timeout: timeout,
	}
	d := net.Dialer{Timeout: timeout}
	rawConn, err := d.DialContext(ctx, "tcp", o.Target.Addr())
	if err != nil {
		return nil, err
	}
	rawConn.SetDeadline(time.Now().Add(timeout))
	c, chans, reqs, err := ssh.NewClientConn(rawConn, o.Target.Addr(), cfg)
	if err != nil {
		rawConn.Close()
		return nil, err
	}
	rawConn.SetDeadline(time.Time{})
	return &Client{conn: ssh.NewClient(c, chans, reqs), Fingerprint: seen}, nil
}

func (c *Client) Close() error {
	return c.conn.Close()
}

type Result struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

const maxOutput = 4 << 20

type limitedBuffer struct {
	buf bytes.Buffer
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
		} else {
			l.buf.Write(p)
		}
	}
	return len(p), nil
}

func (c *Client) Run(ctx context.Context, cmd string, env map[string]string) (*Result, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	var stdout, stderr limitedBuffer
	sess.Stdout = &stdout
	sess.Stderr = &stderr
	full := cmd
	if len(env) > 0 {
		var b strings.Builder
		for k, v := range env {
			if sess.Setenv(k, v) != nil {
				b.WriteString("export " + k + "=" + ShellQuote(v) + "; ")
			}
		}
		full = b.String() + cmd
	}
	done := make(chan error, 1)
	go func() { done <- sess.Run(full) }()
	select {
	case <-ctx.Done():
		sess.Signal(ssh.SIGKILL)
		sess.Close()
		return nil, ctx.Err()
	case err := <-done:
		res := &Result{Stdout: stdout.buf.String(), Stderr: stderr.buf.String()}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
			return res, nil
		}
		if err != nil {
			return res, err
		}
		return res, nil
	}
}

func (c *Client) SFTP() (*sftp.Client, error) {
	return sftp.NewClient(c.conn)
}

func (c *Client) CheckSFTP() (string, error) {
	s, err := c.SFTP()
	if err != nil {
		return "", err
	}
	defer s.Close()
	return s.Getwd()
}

func ShellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'"'"'`) + "'"
}

const (
	CauseHostKeyChanged = "FINGERPRINT_BERUBAH"
	CauseAuth           = "AUTENTIKASI_GAGAL"
	CauseRefused        = "KONEKSI_DITOLAK"
	CauseTimeout        = "TIMEOUT"
	CauseHost           = "HOST_SALAH"
	CauseOther          = "GAGAL"
)

func Classify(err error) (cause, message string) {
	var hk *HostKeyChangedError
	switch {
	case err == nil:
		return "", ""
	case errors.As(err, &hk):
		return CauseHostKeyChanged, "Fingerprint server SSH berubah sejak terakhir terhubung. Bisa jadi server dipindah Hostinger, atau ada penyadapan. Periksa di hPanel, lalu jalankan 'migrator ssh setup <akun> --terima-host-baru' bila yakin aman."
	case strings.Contains(err.Error(), "unable to authenticate"), strings.Contains(err.Error(), "no supported methods remain"):
		return CauseAuth, "Login SSH ditolak. Kemungkinan public key belum ditempel/salah tempel di hPanel, username salah, atau password salah."
	case errors.Is(err, syscall.ECONNREFUSED), strings.Contains(err.Error(), "connection refused"):
		return CauseRefused, "Koneksi ditolak. Kemungkinan SSH belum diaktifkan di hPanel atau port salah."
	}
	var dnsErr *net.DNSError
	if errors.As(err, &dnsErr) {
		return CauseHost, "Host SSH tidak dikenal. Periksa kembali IP/host di hPanel → SSH Access."
	}
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) {
		return CauseTimeout, "Server tidak merespons. Kemungkinan host/port salah, SSH tidak tersedia di paket ini, atau jaringan terputus."
	}
	return CauseOther, "Koneksi SSH gagal: " + err.Error()
}
