package main

import (
	"os"

	"hostinger-migrator/internal/cli"
)

func main() {
	env := cli.Env{
		Stdin:      os.Stdin,
		Stdout:     os.Stdout,
		Stderr:     os.Stderr,
		ReadSecret: cli.TerminalSecretReader(os.Stdin),
	}
	os.Exit(cli.Execute(os.Args[1:], env))
}
