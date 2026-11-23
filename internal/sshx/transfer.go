package sshx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"strings"

	"golang.org/x/crypto/ssh"
)

type StreamResult struct {
	ExitCode int
	Stderr   string
	Bytes    int64
}

type countingWriter struct {
	w io.Writer
	n int64
}

func (c *countingWriter) Write(p []byte) (int, error) {
	n, err := c.w.Write(p)
	c.n += int64(n)
	return n, err
}

func (c *Client) Stream(ctx context.Context, cmd string, env map[string]string, out io.Writer) (*StreamResult, error) {
	sess, err := c.conn.NewSession()
	if err != nil {
		return nil, err
	}
	defer sess.Close()
	cw := &countingWriter{w: out}
	var stderr limitedBuffer
	sess.Stdout = cw
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
		res := &StreamResult{Stderr: stderr.buf.String(), Bytes: cw.n}
		var exitErr *ssh.ExitError
		if errors.As(err, &exitErr) {
			res.ExitCode = exitErr.ExitStatus()
			return res, nil
		}
		return res, err
	}
}

func (c *Client) Upload(ctx context.Context, localPath, remotePath string) (string, error) {
	s, err := c.SFTP()
	if err != nil {
		return "", err
	}
	defer s.Close()
	if err := s.MkdirAll(path.Dir(remotePath)); err != nil {
		return "", fmt.Errorf("gagal membuat folder %s: %w", path.Dir(remotePath), err)
	}
	src, err := os.Open(localPath)
	if err != nil {
		return "", err
	}
	defer src.Close()
	tmp := remotePath + ".part"
	dst, err := s.Create(tmp)
	if err != nil {
		return "", err
	}
	h := sha256.New()
	reader := io.TeeReader(&ctxReader{ctx: ctx, r: src}, h)
	if _, err := io.Copy(dst, reader); err != nil {
		dst.Close()
		return "", err
	}
	if err := dst.Close(); err != nil {
		return "", err
	}
	s.Remove(remotePath)
	if err := s.Rename(tmp, remotePath); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c *ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}

func (c *Client) RemoteSHA256(ctx context.Context, remotePath string) (string, error) {
	res, err := c.Run(ctx, "sha256sum "+ShellQuote(remotePath), nil)
	if err != nil {
		return "", err
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("sha256sum gagal: %s", strings.TrimSpace(res.Stderr))
	}
	f := strings.Fields(res.Stdout)
	if len(f) == 0 {
		return "", errors.New("output sha256sum kosong")
	}
	return strings.TrimPrefix(f[0], `\`), nil
}

func (c *Client) RemoteSize(remotePath string) (int64, error) {
	s, err := c.SFTP()
	if err != nil {
		return 0, err
	}
	defer s.Close()
	fi, err := s.Stat(remotePath)
	if err != nil {
		return 0, err
	}
	return fi.Size(), nil
}

func (c *Client) ReadFile(remotePath string, max int64) ([]byte, error) {
	s, err := c.SFTP()
	if err != nil {
		return nil, err
	}
	defer s.Close()
	f, err := s.Open(remotePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, max))
}

func (c *Client) WriteFile(remotePath string, data []byte) error {
	s, err := c.SFTP()
	if err != nil {
		return err
	}
	defer s.Close()
	var mode os.FileMode = 0o644
	if fi, err := s.Stat(remotePath); err == nil {
		mode = fi.Mode().Perm()
	}
	tmp := remotePath + ".migrator-tmp"
	f, err := s.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(f, bytes.NewReader(data)); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	s.Chmod(tmp, mode)
	s.Remove(remotePath)
	return s.Rename(tmp, remotePath)
}
