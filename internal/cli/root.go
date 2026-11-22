package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/spf13/cobra"
)

const usageTemplate = `Penggunaan:{{if .Runnable}}
  {{indoUse .UseLine}}{{end}}{{if .HasAvailableSubCommands}}
  {{.CommandPath}} [perintah]{{end}}{{if gt (len .Aliases) 0}}

Alias:
  {{.NameAndAliases}}{{end}}{{if .HasExample}}

Contoh:
{{.Example}}{{end}}{{if .HasAvailableSubCommands}}

Perintah:{{range .Commands}}{{if (or .IsAvailableCommand (eq .Name "help"))}}
  {{rpad .Name .NamePadding }} {{.Short}}{{end}}{{end}}{{end}}{{if .HasAvailableLocalFlags}}

Opsi:
{{.LocalFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableInheritedFlags}}

Opsi global:
{{.InheritedFlags.FlagUsages | trimTrailingWhitespaces}}{{end}}{{if .HasAvailableSubCommands}}

Gunakan "{{.CommandPath}} [perintah] --help" untuk informasi lebih lanjut.{{end}}
`

const helpTemplate = `{{with (or .Long .Short)}}{{. | trimTrailingWhitespaces}}

{{end}}{{if or .Runnable .HasSubCommands}}{{.UsageString}}{{end}}`

func Execute(args []string, env Env) int {
	app := newApp(env)
	defer app.close()
	root := newRootCmd(app)
	root.SetArgs(args)
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	err := root.ExecuteContext(ctx)
	if err == nil {
		return ExitOK
	}
	if errors.Is(err, ErrInputClosed) {
		return ExitOK
	}
	msg := app.redactor.String(translateCobraError(err.Error()))
	fmt.Fprintln(app.env.Stderr, "ERROR: "+msg)
	if app.wizardMode {
		app.prompt.Line("Tekan ENTER untuk menutup jendela...")
	}
	var ee *exitError
	if errors.As(err, &ee) {
		return ee.code
	}
	return ExitError
}

func translateCobraError(msg string) string {
	switch {
	case strings.HasPrefix(msg, "unknown command"):
		return strings.Replace(msg, "unknown command", "perintah tidak dikenal", 1) + ". Jalankan 'migrator --help' untuk daftar perintah."
	case strings.HasPrefix(msg, "unknown flag"):
		return strings.Replace(msg, "unknown flag", "opsi tidak dikenal", 1)
	case strings.HasPrefix(msg, "unknown shorthand flag"):
		return strings.Replace(msg, "unknown shorthand flag", "opsi singkat tidak dikenal", 1)
	case strings.HasPrefix(msg, "flag needs an argument"):
		return strings.Replace(msg, "flag needs an argument", "opsi membutuhkan nilai", 1)
	case strings.HasPrefix(msg, "invalid argument"):
		return strings.Replace(msg, "invalid argument", "nilai tidak valid", 1)
	}
	return msg
}

func init() {
	cobra.AddTemplateFunc("indoUse", func(s string) string {
		return strings.ReplaceAll(s, "[flags]", "[opsi]")
	})
}

func newRootCmd(app *App) *cobra.Command {
	root := &cobra.Command{
		Use:           "migrator",
		Short:         "Hostinger Migration Manager — memindahkan website antar akun Hostinger",
		Long:          "Hostinger Migration Manager\n\nJalankan tanpa perintah untuk membuka wizard menu utama.",
		SilenceErrors: true,
		SilenceUsage:  true,
		Args:          noArgs,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			if cmd.Name() == "version" || cmd.Name() == "help" {
				return nil
			}
			if cmd == cmd.Root() {
				app.wizardMode = true
			}
			return app.open(cmd.Context())
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runWizard(cmd.Context())
		},
	}
	root.PersistentFlags().StringVar(&app.configPath, "config", "config.yaml", "lokasi file konfigurasi")
	root.CompletionOptions.DisableDefaultCmd = true
	root.SetUsageTemplate(usageTemplate)
	root.SetHelpTemplate(helpTemplate)
	root.SetOut(app.env.Stdout)
	root.SetErr(app.env.Stderr)
	root.SetFlagErrorFunc(func(c *cobra.Command, err error) error {
		return fmt.Errorf("%s", translateCobraError(err.Error()))
	})
	root.SetHelpCommand(&cobra.Command{
		Use:   "help [perintah]",
		Short: "Bantuan untuk perintah apa pun",
		RunE: func(c *cobra.Command, args []string) error {
			target, _, err := c.Root().Find(args)
			if target == nil || err != nil {
				return fmt.Errorf("perintah tidak dikenal: %s", strings.Join(args, " "))
			}
			return target.Help()
		},
	})
	root.PersistentFlags().BoolP("help", "h", false, "tampilkan bantuan")
	root.AddCommand(newAccountCmd(app), newSSHCmd(app), newAuditCmd(app), newInventoryCmd(app), newPlanCmd(app), newVersionCmd(app))
	root.AddCommand(newWorkerCmds(app)...)
	root.AddCommand(newCutoverCmds(app)...)
	root.AddCommand(newEmailCmd(app))
	root.AddCommand(newCleanupReportCmds(app)...)
	return root
}

func noArgs(cmd *cobra.Command, args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("perintah tidak dikenal %q untuk %q. Jalankan '%s --help' untuk daftar perintah", args[0], cmd.CommandPath(), cmd.CommandPath())
	}
	return nil
}

func exactArgs(n int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) != n {
			return fmt.Errorf("perintah '%s' membutuhkan %d argumen: %s", cmd.CommandPath(), n, name)
		}
		return nil
	}
}

func maxArgs(n int, name string) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) > n {
			return fmt.Errorf("perintah '%s' menerima paling banyak %d argumen: %s", cmd.CommandPath(), n, name)
		}
		return nil
	}
}

func newVersionCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Tampilkan versi migrator",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			app.printf("migrator %s (%s/%s, %s)\n", Version, runtime.GOOS, runtime.GOARCH, runtime.Version())
			return nil
		},
	}
}
