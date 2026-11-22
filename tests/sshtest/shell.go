package sshtest

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
)

func ShellExec(user, home string, extraEnv []string) func(cmd string, env map[string]string) ExecResult {
	return func(cmd string, env map[string]string) ExecResult {
		if cmd == "echo ok; whoami; pwd" {
			return ExecResult{Stdout: "ok\n" + user + "\n" + home + "\n"}
		}
		c := exec.Command("bash", "-c", cmd)
		c.Dir = home
		c.Env = append(os.Environ(), extraEnv...)
		for k, v := range env {
			c.Env = append(c.Env, k+"="+v)
		}
		var out, errb bytes.Buffer
		c.Stdout = &out
		c.Stderr = &errb
		err := c.Run()
		res := ExecResult{Stdout: out.String(), Stderr: errb.String()}
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			res.Exit = exitErr.ExitCode()
		} else if err != nil {
			res.Exit = 127
			res.Stderr += err.Error()
		}
		return res
	}
}
