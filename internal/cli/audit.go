package cli

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/capability"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

func newAuditCmd(app *App) *cobra.Command {
	return &cobra.Command{
		Use:   "audit [alias]",
		Short: "Audit kapabilitas API dan SSH per akun hosting (read-only)",
		Long:  "Menguji setiap operasi yang dibutuhkan migrasi secara read-only dan mencatat hasilnya:\nSUPPORTED (teruji), MANUAL (ada jalur manual), UNSUPPORTED, atau UNKNOWN (belum terbukti).\nOperasi tulis tidak pernah diuji di sini; operasi yang hanya ada di kontrak tetap UNKNOWN.",
		Args:  maxArgs(1, "[alias]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			alias := ""
			if len(args) == 1 {
				alias = args[0]
			}
			return app.runAudit(cmd.Context(), alias)
		},
	}
}

func (a *App) auditor() (*capability.Auditor, error) {
	reg, err := hostinger.DefaultRegistry()
	if err != nil {
		return nil, err
	}
	return &capability.Auditor{
		Store:    a.store,
		Registry: reg,
		SSH:      a.sshService(),
		Log:      a.log,
		NewClient: func(alias string) (*hostinger.Client, error) {
			token, err := a.secrets.Token(alias)
			if errors.Is(err, secrets.ErrNotFound) {
				return nil, fmt.Errorf("%w untuk alias %s", account.ErrTokenMissing, alias)
			}
			if err != nil {
				return nil, err
			}
			return a.newClient(alias, token)
		},
	}, nil
}

func (a *App) runAudit(ctx context.Context, alias string) error {
	creds, err := a.store.ListCredentials(ctx)
	if err != nil {
		return err
	}
	var aliases []string
	for _, c := range creds {
		if alias == "" || c.Alias == alias {
			aliases = append(aliases, c.Alias)
		}
	}
	if len(aliases) == 0 {
		if alias != "" {
			return needsAction(fmt.Errorf("%w: %s", account.ErrAliasNotFound, alias))
		}
		return needsAction(errors.New("belum ada token terdaftar; jalankan 'migrator account add'"))
	}
	aud, err := a.auditor()
	if err != nil {
		return err
	}
	var blocking []error
	for _, al := range aliases {
		a.printf("\nAUDIT KAPABILITAS — token %s\n", al)
		reports, err := aud.AuditAlias(ctx, al)
		for _, rep := range reports {
			a.printReport(rep)
		}
		if err != nil {
			if errors.Is(err, capability.ErrTokenRejected) {
				a.store.WithTx(ctx, func(tx *sql.Tx) error {
					return a.store.UpdateCredentialStatus(ctx, tx, al, store.CredentialRejected, a.redactor.String(err.Error()))
				})
			}
			if errors.Is(err, capability.ErrTokenRejected) || errors.Is(err, account.ErrTokenMissing) {
				blocking = append(blocking, err)
				a.printError(err.Error())
				continue
			}
			return err
		}
	}
	a.println()
	a.println("Keterangan: SUPPORTED = teruji nyata · MANUAL = ada jalur manual · UNSUPPORTED = tidak tersedia · UNKNOWN = belum terbukti (tidak pernah dianggap SUPPORTED).")
	a.println("Hasil tersimpan di database (tabel capabilities).")
	if len(blocking) > 0 {
		return needsAction(errors.Join(blocking...))
	}
	return nil
}

func (a *App) printReport(rep capability.Report) {
	a.printf("\nAkun %s (%s)\n", rep.AccountID, PlanLabel(rep.Plan))
	tw := tabwriter.NewWriter(a.out(), 0, 0, 2, ' ', 0)
	fmt.Fprintf(tw, "KODE\t%s\tCATATAN\n", a.statusCell("STATUS"))
	for _, r := range rep.Results {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", r.Code, a.statusCell(r.Status), a.redactor.String(r.Note))
	}
	tw.Flush()
	a.printf("Ringkasan: SUPPORTED %d · MANUAL %d · UNSUPPORTED %d · UNKNOWN %d\n",
		rep.Count(capability.Supported), rep.Count(capability.Manual), rep.Count(capability.Unsupported), rep.Count(capability.Unknown))
}
