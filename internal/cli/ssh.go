package cli

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/secrets"
	"hostinger-migrator/internal/sshx"
	"hostinger-migrator/internal/store"
)

func (a *App) sshService() *sshx.Service {
	return &sshx.Service{
		Store:   a.store,
		Secrets: a.secrets,
		KeyDir:  a.sshKeyDir(),
		Timeout: 30 * time.Second,
		Log:     a.log,
	}
}

func (a *App) sshKeyDir() string {
	return filepath.Join(a.dataDir, "ssh")
}

func newSSHCmd(app *App) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ssh",
		Short: "Siapkan dan uji akses SSH ke akun hosting",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return cmd.Help()
		},
	}
	var acceptNew bool
	setup := &cobra.Command{
		Use:     "setup [akun]",
		Short:   "Buat SSH key, pandu penempelan di hPanel, lalu tes koneksi",
		Long:    "Membuat satu pasangan SSH key ed25519 (sekali saja), menampilkan public key untuk ditempel di hPanel\nsetiap akun hosting, meminta IP/port/username SSH, lalu menguji koneksi SSH dan SFTP.\nTanpa argumen, semua akun yang SSH-nya belum siap akan diproses.",
		Example: "  migrator ssh setup\n  migrator ssh setup u494983133\n  migrator ssh setup u494983133 --terima-host-baru",
		Args:    maxArgs(1, "[akun]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			account := ""
			if len(args) == 1 {
				account = args[0]
			}
			return app.sshSetup(cmd.Context(), account, acceptNew)
		},
	}
	setup.Flags().BoolVar(&acceptNew, "terima-host-baru", false, "terima fingerprint server SSH yang berubah (setelah diperiksa)")
	test := &cobra.Command{
		Use:   "test [akun]",
		Short: "Uji ulang koneksi SSH dan SFTP",
		Args:  maxArgs(1, "[akun]"),
		RunE: func(cmd *cobra.Command, args []string) error {
			account := ""
			if len(args) == 1 {
				account = args[0]
			}
			return app.sshTest(cmd.Context(), account)
		},
	}
	removePw := &cobra.Command{
		Use:   "remove-password <akun>",
		Short: "Hapus password SSH cadangan dari Credential Manager",
		Args:  exactArgs(1, "<akun>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.sshRemovePassword(cmd.Context(), args[0])
		},
	}
	cmd.AddCommand(setup, test, removePw)
	return cmd
}

func (a *App) sshSetup(ctx context.Context, account string, acceptNew bool) error {
	svc := a.sshService()
	kp, err := svc.Keys()
	if err != nil {
		return err
	}
	if kp.Created {
		a.println("SSH key baru dibuat (ed25519).")
	} else {
		a.println("Memakai SSH key yang sudah ada.")
	}
	a.printf("Private key : %s (hanya bisa dibaca user Windows ini, jangan dibagikan)\n", kp.PrivatePath)
	a.printf("Public key  : %s\n", kp.PublicPath)
	all, err := a.store.ListSSH(ctx)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return needsAction(store.ErrNoAccount)
	}
	var targets []store.SSHInfo
	for _, info := range all {
		if account != "" {
			if info.AccountID == account {
				targets = append(targets, info)
			}
			continue
		}
		if info.Status != store.SSHReady {
			targets = append(targets, info)
		}
	}
	if account != "" && len(targets) == 0 {
		return needsAction(fmt.Errorf("akun hosting %s tidak ditemukan; akun terdaftar: %s", account, accountIDs(all)))
	}
	if len(targets) == 0 {
		a.println("Semua akun hosting sudah SSH_READY. Gunakan 'migrator ssh setup <akun>' untuk mengatur ulang satu akun.")
		return nil
	}
	pending := 0
	for _, info := range targets {
		ok, err := a.setupOne(ctx, svc, kp, info, acceptNew)
		if err != nil {
			return err
		}
		if !ok {
			pending++
		}
	}
	a.println()
	if pending > 0 {
		return needsAction(fmt.Errorf("%d akun belum SSH_READY; lihat 'migrator ssh test'", pending))
	}
	a.println("Semua akun yang diproses sudah SSH_READY.")
	return nil
}

func accountIDs(all []store.SSHInfo) string {
	var ids []string
	for _, i := range all {
		ids = append(ids, i.AccountID)
	}
	return strings.Join(ids, ", ")
}

func (a *App) setupOne(ctx context.Context, svc *sshx.Service, kp *sshx.KeyPair, info store.SSHInfo, acceptNew bool) (bool, error) {
	a.println()
	a.printf("AKTIFKAN SSH — akun %s (%s, token: %s)\n", info.AccountID, PlanLabel(info.Plan), strings.Join(info.Aliases, ", "))
	a.println("1. hPanel → Advanced → SSH Access")
	a.println("2. Pastikan status SSH: Enabled")
	a.println("3. Klik \"Add SSH Key\", tempel baris berikut:")
	a.println()
	a.println(kp.AuthorizedKey)
	a.println()
	a.println("4. Catat IP, Port, dan Username yang tertera di halaman yang sama.")
	usePassword := false
	for {
		v, err := a.prompt.Line("Setelah selesai, ketik SELESAI (PASSWORD = pakai password SSH, LEWATI = lewati akun ini): ")
		if err != nil {
			return false, err
		}
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "SELESAI":
		case "PASSWORD":
			usePassword = true
		case "LEWATI":
			a.printf("Akun %s dilewati.\n", info.AccountID)
			return false, nil
		default:
			continue
		}
		break
	}
	for {
		if err := a.askSSHTarget(&info); err != nil {
			return false, err
		}
		if usePassword {
			pw, err := a.prompt.Secret("Password SSH: ")
			if err != nil {
				return false, err
			}
			if pw == "" {
				a.println("Password kosong, memakai SSH key saja.")
			} else if err := a.secrets.SetSSHPassword(info.AccountID, pw); err != nil {
				return false, err
			}
		}
		a.printf("Menguji koneksi ke %s:%d sebagai %s... ", info.Host, info.Port, info.User)
		res, err := svc.Check(ctx, info, acceptNew)
		if err != nil {
			a.println("GAGAL")
			return false, err
		}
		if res.Ready() {
			a.println("OK")
			a.printf("whoami %s · home %s · SFTP OK · login dengan %s\n", res.Whoami, res.Home, authLabel(res.Info.Auth))
			if res.NewHostKey {
				a.printf("Fingerprint server %s disimpan; koneksi berikutnya diverifikasi terhadap nilai ini.\n", res.Fingerprint)
			}
			a.printf("Status: %s\n", store.SSHReady)
			return true, nil
		}
		a.println("GAGAL")
		a.printf("Penyebab (%s): %s\n", res.Cause, res.Message)
		if res.Cause == sshx.CauseHostKeyChanged {
			return false, nil
		}
		info = res.Info
		v, err := a.prompt.Line("Ketik ULANG untuk mencoba lagi, PASSWORD untuk memakai password SSH, atau LEWATI: ")
		if err != nil {
			return false, err
		}
		switch strings.ToUpper(strings.TrimSpace(v)) {
		case "ULANG":
			usePassword = false
		case "PASSWORD":
			usePassword = true
		default:
			a.printf("Akun %s dilewati (status %s).\n", info.AccountID, res.Info.Status)
			return false, nil
		}
	}
}

func (a *App) askSSHTarget(info *store.SSHInfo) error {
	host, err := a.askDefault("IP / host SSH", info.Host)
	if err != nil {
		return err
	}
	info.Host = host
	for {
		def := ""
		if info.Port > 0 {
			def = strconv.Itoa(info.Port)
		}
		v, err := a.askDefault("Port SSH", def)
		if err != nil {
			return err
		}
		p, convErr := strconv.Atoi(v)
		if convErr != nil || p < 1 || p > 65535 {
			a.println("Port harus angka 1-65535, salin dari hPanel → SSH Access.")
			continue
		}
		info.Port = p
		break
	}
	def := info.User
	if def == "" {
		def = info.AccountID
	}
	user, err := a.askDefault("Username SSH", def)
	if err != nil {
		return err
	}
	info.User = user
	return nil
}

func (a *App) askDefault(label, def string) (string, error) {
	for {
		prompt := label + ": "
		if def != "" {
			prompt = fmt.Sprintf("%s [%s]: ", label, def)
		}
		v, err := a.prompt.Line(prompt)
		if err != nil {
			return "", err
		}
		v = strings.TrimSpace(v)
		if v == "" {
			v = def
		}
		if v != "" {
			return v, nil
		}
		a.printf("%s wajib diisi.\n", label)
	}
}

func authLabel(auth string) string {
	if auth == store.SSHAuthPassword {
		return "password"
	}
	return "SSH key"
}

func (a *App) sshTest(ctx context.Context, account string) error {
	svc := a.sshService()
	all, err := a.store.ListSSH(ctx)
	if err != nil {
		return err
	}
	if len(all) == 0 {
		return needsAction(store.ErrNoAccount)
	}
	tw := tabwriter.NewWriter(a.out(), 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "AKUN\tSTATUS\tHOST:PORT\tUSER\tLOGIN\tCATATAN")
	problems := 0
	found := false
	for _, info := range all {
		if account != "" && info.AccountID != account {
			continue
		}
		found = true
		if info.Host == "" {
			problems++
			fmt.Fprintf(tw, "%s\t%s\t-\t-\t-\tjalankan 'migrator ssh setup %s'\n", info.AccountID, info.Status, info.AccountID)
			continue
		}
		res, err := svc.Check(ctx, info, false)
		if err != nil {
			return err
		}
		note := "SFTP OK"
		if !res.Ready() {
			problems++
			note = res.Message
		}
		fmt.Fprintf(tw, "%s\t%s\t%s:%d\t%s\t%s\t%s\n", info.AccountID, res.Info.Status, info.Host, info.Port, info.User, authLabel(res.Info.Auth), note)
	}
	tw.Flush()
	if account != "" && !found {
		return needsAction(fmt.Errorf("akun hosting %s tidak ditemukan", account))
	}
	if problems > 0 {
		return needsAction(fmt.Errorf("%d akun belum SSH_READY", problems))
	}
	return nil
}

func (a *App) sshRemovePassword(ctx context.Context, account string) error {
	err := a.secrets.DeleteSSHPassword(account)
	if errors.Is(err, secrets.ErrNotFound) {
		a.printf("Tidak ada password SSH tersimpan untuk akun %s.\n", account)
		return nil
	}
	if err != nil {
		return err
	}
	a.store.AppendAudit(ctx, nil, "operator", "ssh.remove_password", account, "OK", "password SSH cadangan dihapus dari Credential Manager")
	a.printf("Password SSH akun %s dihapus dari Credential Manager.\n", account)
	if info, err := a.store.GetSSH(ctx, account); err == nil && info.Auth == store.SSHAuthPassword {
		a.printf("PERHATIAN: akun %s sebelumnya login dengan password. Pastikan public key sudah ditempel, lalu jalankan 'migrator ssh test %s'.\n", account, account)
	}
	return nil
}
