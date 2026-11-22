package sshx

import (
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
)

func restrictToOwner(path string) error {
	if runtime.GOOS != "windows" {
		if err := os.Chmod(path, 0o600); err != nil {
			return fmt.Errorf("gagal membatasi izin %s: %w", path, err)
		}
		return nil
	}
	u, err := user.Current()
	if err != nil {
		return fmt.Errorf("gagal mengetahui user Windows saat ini: %w", err)
	}
	icacls := "icacls.exe"
	if root := os.Getenv("SystemRoot"); root != "" {
		icacls = filepath.Join(root, "System32", "icacls.exe")
	}
	out, err := exec.Command(icacls, path, "/inheritance:r", "/grant:r", u.Username+":F").CombinedOutput()
	if err != nil {
		return fmt.Errorf("gagal membatasi izin %s ke user %s: %v: %s", path, u.Username, err, string(out))
	}
	return nil
}
