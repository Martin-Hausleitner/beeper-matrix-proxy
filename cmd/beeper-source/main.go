package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Martin-Hausleitner/beeper-matrix-proxy/beepersource"
)

func main() {
	dbPath := flag.String("db", "beeper-source.db", "SQLite WAL database path")
	once := flag.Bool("once", false, "run one reconcile pass and exit")
	roomsOnly := flag.Bool("rooms-only", false, "create/update Matrix portal rooms without importing messages")
	backfillHistory := flag.Bool("backfill-history", false, "crawl older Beeper messages into existing Matrix portal rooms without Matrix-to-Beeper sends")
	historyChatLimit := flag.Int("history-chat-limit", 8, "maximum chats to backfill by one page in a single history pass")
	interval := flag.Duration("interval", 30*time.Second, "reconcile interval")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg := beepersource.DefaultConfig()
	if *roomsOnly {
		applyRoomsOnlySafety(&cfg)
	}
	if *backfillHistory {
		applyBackfillHistorySafety(&cfg)
	}
	beeperToken, err := cfg.BeeperToken()
	exitIfErr("load Beeper token", err)
	matrixToken, err := cfg.MatrixToken()
	exitIfErr("load Matrix token", err)

	store, err := beepersource.OpenStore(ctx, *dbPath)
	exitIfErr("open store", err)
	defer store.Close()

	api := beepersource.NewDesktopAPIAdapter(cfg, beeperToken)
	matrix, err := beepersource.NewMatrixClientSink(cfg, store, matrixToken)
	exitIfErr("create Matrix sink", err)
	svc := beepersource.NewService(cfg, store, api, matrix)
	matrixSource := beepersource.NewMatrixClientSource(cfg, store, matrixToken)

	run := func() {
		if *backfillHistory {
			result, err := svc.BackfillHistoryOnce(ctx, *historyChatLimit)
			if err != nil {
				fmt.Fprintf(os.Stderr, "history backfill completed with errors: %v\n", err)
			}
			fmt.Fprintf(os.Stderr, "history backfill pass: considered=%d processed=%d messages_seen=%d messages_mirrored=%d completed=%d failed=%d\n",
				result.ChatsConsidered, result.ChatsProcessed, result.MessagesSeen, result.MessagesMirrored, result.CompletedChats, result.FailedChats)
			return
		}
		if *roomsOnly {
			if err := svc.ReconcilePortalsOnly(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "rooms-only reconcile failed: %v\n", err)
				return
			}
			fmt.Fprintln(os.Stderr, "rooms-only reconcile completed")
			return
		}
		if err := svc.ReconcileOnce(ctx); err != nil {
			fmt.Fprintf(os.Stderr, "reconcile failed: %v\n", err)
			return
		}
		handled, err := matrixSource.SyncOnce(ctx, svc)
		if err != nil {
			fmt.Fprintf(os.Stderr, "matrix sync failed: %v\n", err)
			return
		}
		if handled > 0 {
			if err := svc.ReconcileOnce(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "post-sync reconcile failed: %v\n", err)
				return
			}
		}
		fmt.Fprintln(os.Stderr, "reconcile completed")
	}
	run()
	if *once {
		return
	}

	ticker := time.NewTicker(*interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}

func exitIfErr(label string, err error) {
	if err != nil {
		fmt.Fprintf(os.Stderr, "%s: %v\n", label, err)
		os.Exit(1)
	}
}

func applyRoomsOnlySafety(cfg *beepersource.Config) {
	cfg.Sync.Mode = beepersource.SyncModeReadOnly
	cfg.Safety.DisableMatrixToBeeper = true
	appendExcludedAccountIDs(cfg, "sh-vcvm-matrix", "matrix", "beeper-matrix-proxy")
	if _, explicit := os.LookupEnv("BEEPER_MATRIX_PROXY_MATRIX_SPACES"); !explicit {
		cfg.Matrix.Spaces = true
	}
	if _, explicit := os.LookupEnv("BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX"); !explicit {
		cfg.Matrix.RoomNamePrefix = ""
	}
	cfg.Matrix.RoomNameIncludePlatform = false
}

func applyBackfillHistorySafety(cfg *beepersource.Config) {
	cfg.Sync.Mode = beepersource.SyncModeReadOnly
	cfg.Safety.DisableMatrixToBeeper = true
	appendExcludedAccountIDs(cfg, "sh-vcvm-matrix", "matrix", "beeper-matrix-proxy")
}

func appendExcludedAccountIDs(cfg *beepersource.Config, accountIDs ...string) {
	for _, accountID := range accountIDs {
		if accountID == "" {
			continue
		}
		seen := false
		for _, existing := range cfg.Beeper.ExcludeAccountIDs {
			if existing == accountID {
				seen = true
				break
			}
		}
		if !seen {
			cfg.Beeper.ExcludeAccountIDs = append(cfg.Beeper.ExcludeAccountIDs, accountID)
		}
	}
}
