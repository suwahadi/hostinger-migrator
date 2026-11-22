package cli

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/pipeline"
	"hostinger-migrator/internal/secrets"
)

func newEmailCmd(app *App) *cobra.Command {
	var batchID string
	cmd := &cobra.Command{
		Use:   "email",
		Short: "Setujui pemindahan email (ganti password sumber, backup, hapus mailbox lama) per batch",
		Long: "Email dipindah setelah cutover website. Karena Hostinger mengharuskan email lama dihapus sebelum tujuan siap,\n" +
			"persetujuan ini mencakup: mengganti password mailbox sumber dengan password baru (untuk backup IMAP),\n" +
			"backup semua pesan, lalu menghapus mailbox sumber setelah backup tervalidasi.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.approveEmail(cmd.Context(), batchID)
		},
	}
	cmd.Flags().StringVar(&batchID, "batch", "", "batch tertentu")
	var domain, pwBatch string
	passwords := &cobra.Command{
		Use:   "passwords",
		Short: "Tampilkan password baru mailbox (hanya di terminal, untuk dibagikan ke pengguna)",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.showMailPasswords(cmd.Context(), pwBatch, domain)
		},
	}
	passwords.Flags().StringVar(&domain, "domain", "", "domain tertentu")
	passwords.Flags().StringVar(&pwBatch, "batch", "", "batch tertentu")
	cmd.AddCommand(passwords)
	return cmd
}

func (a *App) approveEmail(ctx context.Context, batchID string) error {
	batches, err := a.store.ListBatches(ctx)
	if err != nil {
		return err
	}
	any := false
	for _, b := range batches {
		if batchID != "" && b.ID != batchID {
			continue
		}
		items, _ := a.store.ListItems(ctx, b.ID)
		var domains []string
		for _, it := range items {
			if it.State != pipeline.StateEmailRunning {
				continue
			}
			if ok, _ := a.store.IsApproved(ctx, b.ID, gate.KindDeleteEmail, it.SourceDomain); ok {
				continue
			}
			domains = append(domains, it.SourceDomain)
		}
		if len(domains) == 0 {
			continue
		}
		any = true
		a.printf("\nPEMINDAHAN EMAIL batch %s\n", b.ID)
		for _, d := range domains {
			boxes, _ := a.store.ListMailboxes(ctx, []string{d})
			var msgs, kb int64
			for _, m := range boxes {
				msgs += m.MessageCount
				kb += m.UsedKB
			}
			a.printf("  %s: %d mailbox, sekitar %d pesan, %s\n", d, len(boxes), msgs, sizeLabel(kb*1024))
			for _, m := range boxes {
				a.printf("     - %s (%d pesan)\n", m.Address, m.MessageCount)
			}
		}
		a.println("Langkah: password mailbox sumber diganti (password baru) → backup IMAP + validasi jumlah pesan →")
		a.println("mailbox sumber DIHAPUS → Anda mengaktifkan email di akun tujuan → mailbox dibuat ulang → pesan dipulihkan.")
		a.println("Selama proses, email domain tersebut tidak bisa menerima pesan (downtime disetujui di PRD).")
		ok, err := a.prompt.Confirm("Ketik HAPUS EMAIL untuk menyetujui: ", gate.Words[gate.KindDeleteEmail])
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan untuk batch ini.")
			continue
		}
		if err := gate.Request(ctx, a.store, b.ID, gate.KindDeleteEmail, domains, gate.Words[gate.KindDeleteEmail]); err != nil {
			return err
		}
		a.printf("Pemindahan email disetujui untuk %d domain.\n", len(domains))
	}
	if !any {
		a.println("Tidak ada domain yang menunggu persetujuan email.")
		return nil
	}
	return a.startWorker(ctx, false)
}

func (a *App) showMailPasswords(ctx context.Context, batchID, domain string) error {
	items, err := a.store.ListItems(ctx, batchID)
	if err != nil {
		return err
	}
	shown := 0
	for _, it := range items {
		if domain != "" && it.SourceDomain != domain {
			continue
		}
		boxes, _ := a.store.ListMailboxes(ctx, []string{it.SourceDomain})
		for _, m := range boxes {
			pw, err := a.secrets.Get(secrets.KindMailboxPassword, m.Address)
			if err != nil {
				continue
			}
			if shown == 0 {
				a.println("Password baru mailbox (bagikan langsung ke pemilik mailbox, jangan simpan di chat/dokumen):")
			}
			fmt.Fprintf(a.env.Stdout, "  %-40s %s\n", m.Address, pw)
			shown++
		}
	}
	if shown == 0 {
		a.println("Belum ada password mailbox baru.")
		return nil
	}
	a.store.AppendAudit(ctx, nil, "operator", "email.passwords_shown", domain, "OK", fmt.Sprintf("%d password ditampilkan", shown))
	return nil
}
