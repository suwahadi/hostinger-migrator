package sshx

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/crypto/ssh"
)

const KeyComment = "hostinger-migrator"

type KeyPair struct {
	Signer        ssh.Signer
	AuthorizedKey string
	PrivatePath   string
	PublicPath    string
	Created       bool
}

func EnsureKeyPair(dir string) (*KeyPair, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("gagal membuat folder SSH: %w", err)
	}
	priv := filepath.Join(dir, "id_ed25519")
	pub := priv + ".pub"
	if data, err := os.ReadFile(priv); err == nil {
		signer, err := ssh.ParsePrivateKey(data)
		if err != nil {
			return nil, fmt.Errorf("private key %s rusak: %w", priv, err)
		}
		if err := restrictToOwner(priv); err != nil {
			return nil, err
		}
		line := authorizedLine(signer.PublicKey())
		if err := os.WriteFile(pub, []byte(line+"\n"), 0o644); err != nil {
			return nil, fmt.Errorf("gagal menulis public key: %w", err)
		}
		return &KeyPair{Signer: signer, AuthorizedKey: line, PrivatePath: priv, PublicPath: pub}, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("gagal membaca private key: %w", err)
	}
	pubKey, privKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	block, err := ssh.MarshalPrivateKey(privKey, KeyComment)
	if err != nil {
		return nil, err
	}
	tmp := priv + ".tmp"
	if err := os.WriteFile(tmp, pem.EncodeToMemory(block), 0o600); err != nil {
		return nil, fmt.Errorf("gagal menulis private key: %w", err)
	}
	if err := restrictToOwner(tmp); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	if err := os.Rename(tmp, priv); err != nil {
		os.Remove(tmp)
		return nil, err
	}
	sshPub, err := ssh.NewPublicKey(pubKey)
	if err != nil {
		return nil, err
	}
	line := authorizedLine(sshPub)
	if err := os.WriteFile(pub, []byte(line+"\n"), 0o644); err != nil {
		return nil, fmt.Errorf("gagal menulis public key: %w", err)
	}
	signer, err := ssh.NewSignerFromKey(privKey)
	if err != nil {
		return nil, err
	}
	return &KeyPair{Signer: signer, AuthorizedKey: line, PrivatePath: priv, PublicPath: pub, Created: true}, nil
}

func authorizedLine(k ssh.PublicKey) string {
	return strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k))) + " " + KeyComment
}

func Fingerprint(k ssh.PublicKey) string {
	return ssh.FingerprintSHA256(k)
}
