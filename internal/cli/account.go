package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/account"
	"hostinger-migrator/internal/hostinger"
	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/store"
)

func newAccountCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "account",
		Short: "Kelola API token Hostinger (tambah, daftar, tes, hapus)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	var alias string
	var tokenStdin bool
	add := &cobra.Command{
		Use:     "add",
		Short:   "Daftarkan API token baru dengan alias",
		Long:    "Daftarkan API token Hostinger baru.\n\nToken divalidasi dengan endpoint read-only, akun hosting di baliknya ditemukan otomatis,\nlalu token disimpan di Windows Credential Manager. Token tidak pernah ditulis ke database, log, atau laporan.",
		Example: "  migrator account add\n  migrator account add --alias xyz\n  printf '%s\\n' \"$TOKEN\" | migrator account add --alias xyz --token-stdin",
		Args:    noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.accountAdd(cmd.Context(), alias, tokenStdin)
		},
	}
	add.Flags().StringVar(&alias, "alias", "", "nama alias token (huruf kecil, angka, '-' atau '_')")
	add.Flags().BoolVar(&tokenStdin, "token-stdin", false, "baca token dari stdin tanpa prompt")

	list := &cobra.Command{
		Use:   "list",
		Short: "Tampilkan token terdaftar dan akun hosting di baliknya",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.accountList(cmd.Context())
		},
	}
	test := &cobra.Command{
		Use:   "test <alias>",
		Short: "Uji ulang token dan perbarui daftar akun hosting",
		Args:  exactArgs(1, "<alias>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.accountTest(cmd.Context(), args[0])
		},
	}
	var yes bool
	remove := &cobra.Command{
		Use:   "remove <alias>",
		Short: "Hapus token dari komputer ini (tidak menyentuh Hostinger)",
		Args:  exactArgs(1, "<alias>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.accountRemove(cmd.Context(), args[0], yes)
		},
	}
	remove.Flags().BoolVar(&yes, "ya", false, "lewati konfirmasi")
	cmd.AddCommand(add, list, test, remove)
	return cmd
}

func (a *App) accountAdd(ctx context.Context, alias string, tokenStdin bool) error {
	var token string
	var err error
	if tokenStdin {
		token, err = a.prompt.in.ReadString('\n')
		if err != nil && token == "" {
			return errors.New("token tidak terbaca dari stdin")
		}
		token = strings.TrimSpace(token)
	} else {
		token, err = a.prompt.Secret("Masukkan API token: ")
		if err != nil {
			return err
		}
	}
	if err := secrets.ValidateToken(token); err != nil {
		return err
	}
	a.redactor.Add(token)
	for alias == "" {
		v, err := a.prompt.Line("Nama token ini: ")
		if err != nil {
			return err
		}
		alias = strings.TrimSpace(v)
		if alias != "" {
			if verr := secrets.ValidateAlias(alias); verr != nil {
				a.println(verr.Error())
				alias = ""
			}
		}
	}
	a.printf("Memvalidasi token... ")
	res, err := a.accounts().Add(ctx, alias, token)
	if err != nil {
		a.println("GAGAL")
		if errors.Is(err, account.ErrTokenRejected) || errors.Is(err, account.ErrAliasExists) {
			return needsAction(err)
		}
		return err
	}
	a.println("OK")
	a.println()
	printDiscovery(a, alias, res.Discovery)
	a.printf("Token %q tersimpan di Windows Credential Manager.\n", alias)
	return nil
}

func printDiscovery(a *App, alias string, d *hostinger.Discovery) {
	if d == nil {
		return
	}
	if len(d.Accounts) == 0 {
		a.printf("Tidak ada akun hosting dengan website ditemukan di token %s.\n", alias)
	} else {
		a.printf("Ditemukan %d akun di token %s:\n", len(d.Accounts), alias)
		for i, acc := range d.Accounts {
			a.printf("[%d] %s  (%s) · %d website\n", i+1, acc.Username, PlanLabel(acc.Plan), acc.WebsiteCount)
		}
	}
	if len(d.OrdersWithoutSite) > 0 {
		a.printf("Catatan: %d order hosting belum memiliki website (akun hosting belum disiapkan):\n", len(d.OrdersWithoutSite))
		for _, o := range d.OrdersWithoutSite {
			a.printf("    order %d  (%s, status %s)\n", o.ID, PlanLabel(o.Plan.Name), o.Status)
		}
	}
	a.println()
}

func PlanLabel(name string) string {
	switch strings.ToLower(name) {
	case "":
		return "paket tidak diketahui"
	case "hostinger_single":
		return "Single"
	case "hostinger_premium":
		return "Premium"
	case "hostinger_business":
		return "Business"
	}
	return name
}

func (a *App) accountList(ctx context.Context) error {
	items, err := a.accounts().List(ctx)
	if err != nil {
		return err
	}
	if len(items) == 0 {
		a.println("Belum ada token terdaftar. Tambahkan dengan: migrator account add")
		return nil
	}
	tw := tabwriter.NewWriter(a.out(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "ALIAS\tSTATUS\tDIUJI TERAKHIR\tAKUN HOSTING")
	for _, it := range items {
		var accs []string
		for _, acc := range it.Accounts {
			accs = append(accs, fmt.Sprintf("%s (%s, %d web)", acc.ID, PlanLabel(acc.Plan), acc.WebsiteCount))
		}
		if len(accs) == 0 {
			accs = []string{"-"}
		}
		checked := it.Credential.LastCheckedAt
		if checked == "" {
			checked = "-"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", it.Credential.Alias, it.Credential.LastStatus, checked, strings.Join(accs, ", "))
	}
	tw.Flush()
	for _, it := range items {
		if it.Credential.LastStatus == store.CredentialRejected {
			a.printf("\nToken %q DITOLAK Hostinger (%s). Buat token baru di hPanel, lalu jalankan 'migrator account remove %s' dan 'migrator account add --alias %s'.\n",
				it.Credential.Alias, a.redactor.String(it.Credential.LastError), it.Credential.Alias, it.Credential.Alias)
		}
	}
	return nil
}

func (a *App) accountTest(ctx context.Context, alias string) error {
	a.printf("Menguji token %s... ", alias)
	res, err := a.accounts().Test(ctx, alias)
	if err != nil {
		a.println("GAGAL")
		if errors.Is(err, account.ErrTokenRejected) || errors.Is(err, account.ErrAliasNotFound) || errors.Is(err, account.ErrTokenMissing) {
			return needsAction(err)
		}
		return err
	}
	a.println("OK")
	a.println()
	printDiscovery(a, alias, res.Discovery)
	return nil
}

func (a *App) accountRemove(ctx context.Context, alias string, yes bool) error {
	if !yes {
		a.printf("Token %q akan dihapus dari komputer ini saja. Hostinger tidak disentuh.\n", alias)
		ok, err := a.prompt.Confirm(fmt.Sprintf("Ketik %s untuk konfirmasi: ", alias), alias)
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan.")
			return nil
		}
	}
	if err := a.accounts().Remove(ctx, alias); err != nil {
		if errors.Is(err, account.ErrAliasNotFound) {
			return needsAction(err)
		}
		return err
	}
	a.printf("Token %q dihapus dari Credential Manager dan database lokal.\n", alias)
	return nil
}
