package sshtest

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync"

	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type ExecResult struct {
	Stdout string
	Stderr string
	Exit   int
}

type Server struct {
	listener   net.Listener
	config     *ssh.ServerConfig
	HostSigner ssh.Signer
	User       string
	mu         sync.Mutex
	authorized []ssh.PublicKey
	password   string
	Exec       func(cmd string, env map[string]string) ExecResult
	commands   []string
	wg         sync.WaitGroup
}

func New(user string) (*Server, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return nil, err
	}
	s := &Server{HostSigner: signer, User: user}
	s.Exec = s.defaultExec
	cfg := &ssh.ServerConfig{
		PublicKeyCallback: func(meta ssh.ConnMetadata, key ssh.PublicKey) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if meta.User() != s.User {
				return nil, errDenied
			}
			for _, k := range s.authorized {
				if bytes.Equal(k.Marshal(), key.Marshal()) {
					return &ssh.Permissions{}, nil
				}
			}
			return nil, errDenied
		},
		PasswordCallback: func(meta ssh.ConnMetadata, pw []byte) (*ssh.Permissions, error) {
			s.mu.Lock()
			defer s.mu.Unlock()
			if meta.User() == s.User && s.password != "" && string(pw) == s.password {
				return &ssh.Permissions{}, nil
			}
			return nil, errDenied
		},
	}
	cfg.AddHostKey(signer)
	s.config = cfg
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	s.listener = l
	go s.serve()
	return s, nil
}

type deniedError struct{}

func (deniedError) Error() string { return "ditolak" }

var errDenied = deniedError{}

func (s *Server) Host() string { return "127.0.0.1" }

func (s *Server) Port() int {
	_, p, _ := net.SplitHostPort(s.listener.Addr().String())
	n, _ := strconv.Atoi(p)
	return n
}

func (s *Server) Authorize(k ssh.PublicKey) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.authorized = append(s.authorized, k)
}

func (s *Server) SetPassword(pw string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.password = pw
}

func (s *Server) RotateHostKey() error {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	signer, err := ssh.NewSignerFromKey(priv)
	if err != nil {
		return err
	}
	cfg := *s.config
	cfg.AddHostKey(signer)
	s.mu.Lock()
	s.config = &cfg
	s.HostSigner = signer
	s.mu.Unlock()
	return nil
}

func (s *Server) Commands() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.commands...)
}

func (s *Server) Close() error {
	err := s.listener.Close()
	s.wg.Wait()
	return err
}

func (s *Server) defaultExec(cmd string, env map[string]string) ExecResult {
	return ExecResult{Stdout: "", Exit: 0}
}

func (s *Server) serve() {
	for {
		c, err := s.listener.Accept()
		if err != nil {
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			s.handle(c)
		}()
	}
}

func (s *Server) handle(c net.Conn) {
	s.mu.Lock()
	cfg := s.config
	s.mu.Unlock()
	sc, chans, reqs, err := ssh.NewServerConn(c, cfg)
	if err != nil {
		c.Close()
		return
	}
	defer sc.Close()
	go ssh.DiscardRequests(reqs)
	for nc := range chans {
		if nc.ChannelType() != "session" {
			nc.Reject(ssh.UnknownChannelType, "tidak didukung")
			continue
		}
		ch, chReqs, err := nc.Accept()
		if err != nil {
			continue
		}
		go s.session(ch, chReqs)
	}
}

func (s *Server) session(ch ssh.Channel, reqs <-chan *ssh.Request) {
	defer ch.Close()
	env := map[string]string{}
	for req := range reqs {
		switch req.Type {
		case "env":
			var kv struct{ Name, Value string }
			ssh.Unmarshal(req.Payload, &kv)
			env[kv.Name] = kv.Value
			req.Reply(true, nil)
		case "exec":
			var p struct{ Command string }
			ssh.Unmarshal(req.Payload, &p)
			req.Reply(true, nil)
			s.mu.Lock()
			s.commands = append(s.commands, p.Command)
			exec := s.Exec
			s.mu.Unlock()
			res := exec(p.Command, env)
			io.WriteString(ch, res.Stdout)
			io.WriteString(ch.Stderr(), res.Stderr)
			status := make([]byte, 4)
			binary.BigEndian.PutUint32(status, uint32(res.Exit))
			ch.SendRequest("exit-status", false, status)
			return
		case "subsystem":
			var p struct{ Name string }
			ssh.Unmarshal(req.Payload, &p)
			if p.Name != "sftp" {
				req.Reply(false, nil)
				continue
			}
			req.Reply(true, nil)
			srv, err := sftp.NewServer(ch, sftp.WithServerWorkingDirectory("/"))
			if err != nil {
				return
			}
			srv.Serve()
			return
		default:
			if req.WantReply {
				req.Reply(false, nil)
			}
		}
	}
}
