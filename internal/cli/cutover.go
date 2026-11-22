package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"hostinger-migrator/internal/cutover"
	"hostinger-migrator/internal/gate"
	"hostinger-migrator/internal/pipeline"
)

func newCutoverCmds(app *App) []*cobra.Command {
	var batchID, item string
	cut := &cobra.Command{
		Use:   "cutover",
		Short: "Pindahkan domain ke tujuan untuk website VERIFIED (satu konfirmasi per batch)",
		Long: "Untuk setiap website VERIFIED: backup final + restore ulang, snapshot DNS, lepas domain di sumber (manual),\n" +
			"pasang domain di tujuan, ubah A/AAAA ke IP tujuan (record lain dipertahankan), pasang SSL, verifikasi akhir.\n" +
			"Website DUMMY_ONLY tidak ikut. Bisa dibatalkan sebelum cleanup dengan 'migrator rollback <domain>'.",
		Args: noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runCutover(cmd.Context(), batchID, item)
		},
	}
	cut.Flags().StringVar(&batchID, "batch", "", "batch tertentu")
	cut.Flags().StringVar(&item, "item", "", "hanya satu website (domain)")
	var rbBatch string
	rollback := &cobra.Command{
		Use:   "rollback <domain>",
		Short: "Kembalikan DNS dan domain ke sumber (sebelum cleanup)",
		Args:  exactArgs(1, "<domain>"),
		RunE: func(cmd *cobra.Command, args []string) error {
			return app.runRollback(cmd.Context(), args[0], rbBatch)
		},
	}
	rollback.Flags().StringVar(&rbBatch, "batch", "", "batch tertentu")
	return []*cobra.Command{cut, rollback}
}

func (a *App) runCutover(ctx context.Context, batchID, only string) error {
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
			if it.State != pipeline.StateVerified || it.SideState != "" {
				continue
			}
			if only != "" && it.SourceDomain != only {
				continue
			}
			if ok, _ := a.store.IsApproved(ctx, b.ID, gate.KindCutover, it.SourceDomain); ok {
				continue
			}
			domains = append(domains, it.SourceDomain)
		}
		if len(domains) == 0 {
			continue
		}
		any = true
		a.printf("\nCUTOVER batch %s (%s/%s → %s/%s)\n", b.ID, b.SourceAlias, b.SourceAccount, b.DestAlias, b.DestAccount)
		for _, d := range domains {
			a.printf("  - %s\n", d)
		}
		a.println("Yang akan dilakukan per website:")
		a.println("  1. Backup final + restore ulang ke dummy, verifikasi singkat")
		a.println("  2. Snapshot DNS zone (disimpan lokal untuk rollback)")
		a.println("  3. Anda mengganti domain website sumber ke domain sementara di hPanel (situs lama tetap hidup)")
		a.println("  4. Domain dipasang di website tujuan, record A/AAAA diubah ke IP tujuan (MX, TXT, CNAME pihak ketiga dipertahankan)")
		a.println("  5. SSL dipasang dan verifikasi akhir lewat domain asli")
		ok, err := a.prompt.Confirm("Operasi ini mengubah domain dan DNS. Ketik CUTOVER untuk melanjutkan: ", gate.Words[gate.KindCutover])
		if err != nil {
			return err
		}
		if !ok {
			a.println("Dibatalkan untuk batch ini.")
			continue
		}
		if err := gate.Request(ctx, a.store, b.ID, gate.KindCutover, domains, gate.Words[gate.KindCutover]); err != nil {
			return err
		}
		a.printf("Cutover disetujui untuk %d website.\n", len(domains))
	}
	if !any {
		a.println("Tidak ada website VERIFIED yang menunggu cutover.")
		return nil
	}
	return a.startWorker(ctx, false)
}

func (a *App) runRollback(ctx context.Context, domain, batchID string) error {
	it, err := a.store.FindItem(ctx, domain, batchID)
	if err != nil {
		return needsAction(fmt.Errorf("website %s tidak ada di batch mana pun", domain))
	}
	a.printf("Rollback %s (status %s): DNS dikembalikan dari snapshot, parked domain di tujuan dilepas, persetujuan cutover dicabut.\n", domain, pipeline.ItemLabel(it))
	ok, err := a.prompt.Confirm("Ketik ROLLBACK untuk melanjutkan: ", "ROLLBACK")
	if err != nil {
		return err
	}
	if !ok {
		a.println("Dibatalkan.")
		return nil
	}
	res, err := cutover.Rollback(ctx, a.runner(), it)
	if err != nil {
		return needsAction(err)
	}
	if len(res.DNSRestored) > 0 {
		a.printf("DNS dikembalikan: %s\n", strings.Join(res.DNSRestored, "; "))
	}
	if res.ParkedRemoved {
		a.println("Parked domain di akun tujuan dilepas.")
	}
	if len(res.Manual) > 0 {
		a.println("Langkah manual yang masih perlu Anda kerjakan:")
		for _, m := range res.Manual {
			a.printf("  - %s\n", m)
		}
	}
	a.printf("%s kembali ke status VERIFIED. Jalankan 'migrator cutover' lagi bila siap.\n", domain)
	return nil
}
