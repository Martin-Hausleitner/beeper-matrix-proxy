package main

import (
	"testing"

	"github.com/Martin-Hausleitner/beeper-matrix-proxy/beepersource"
)

func TestApplyRoomsOnlySafetyForcesReadOnlyAndKillSwitch(t *testing.T) {
	cfg := beepersource.DefaultConfig()
	cfg.Sync.Mode = beepersource.SyncModeBidirectional
	cfg.Safety.DisableMatrixToBeeper = false

	applyRoomsOnlySafety(&cfg)

	if cfg.Sync.Mode != beepersource.SyncModeReadOnly {
		t.Fatalf("expected rooms-only mode to force read_only, got %q", cfg.Sync.Mode)
	}
	if !cfg.Safety.DisableMatrixToBeeper {
		t.Fatal("expected rooms-only mode to disable Matrix -> Beeper sends")
	}
	if !cfg.Matrix.Spaces {
		t.Fatal("expected rooms-only mode to enable Matrix spaces")
	}
	if cfg.Matrix.PlatformAvatars {
		t.Fatal("expected rooms-only mode to keep real Beeper avatars preferred")
	}
	if cfg.Matrix.RoomNameIncludePlatform {
		t.Fatal("expected rooms-only mode to omit platform brackets because spaces group rooms by service")
	}
	if cfg.Matrix.RoomNamePrefix != "" {
		t.Fatalf("expected rooms-only mode to omit Beeper room-name prefix, got %q", cfg.Matrix.RoomNamePrefix)
	}
	assertExcludedAccountIDs(t, cfg, "sh-vcvm-matrix", "matrix", "beeper-matrix-proxy")
}

func TestApplyRoomsOnlySafetyRespectsExplicitSpacesOff(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_SPACES", "false")
	cfg := beepersource.DefaultConfig()

	applyRoomsOnlySafety(&cfg)

	if cfg.Matrix.Spaces {
		t.Fatal("expected explicit Matrix spaces=false to be preserved for refresh-only imports")
	}
}

func TestApplyRoomsOnlySafetyRespectsExplicitRoomNamePrefix(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX", "Beeper: ")
	cfg := beepersource.DefaultConfig()

	applyRoomsOnlySafety(&cfg)

	if cfg.Matrix.RoomNamePrefix != "Beeper: " {
		t.Fatalf("expected explicit room name prefix to be preserved, got %q", cfg.Matrix.RoomNamePrefix)
	}
}

func TestBackfillHistorySafetyExcludesMatrixBridgeSources(t *testing.T) {
	cfg := beepersource.DefaultConfig()
	cfg.Beeper.ExcludeAccountIDs = []string{"custom"}

	applyBackfillHistorySafety(&cfg)

	if cfg.Sync.Mode != beepersource.SyncModeReadOnly {
		t.Fatalf("expected history backfill to force read_only, got %q", cfg.Sync.Mode)
	}
	if !cfg.Safety.DisableMatrixToBeeper {
		t.Fatal("expected history backfill to disable Matrix -> Beeper sends")
	}
	assertExcludedAccountIDs(t, cfg, "custom", "sh-vcvm-matrix", "matrix", "beeper-matrix-proxy")
}

func assertExcludedAccountIDs(t *testing.T, cfg beepersource.Config, want ...string) {
	t.Helper()
	seen := make(map[string]bool, len(cfg.Beeper.ExcludeAccountIDs))
	for _, accountID := range cfg.Beeper.ExcludeAccountIDs {
		seen[accountID] = true
	}
	for _, accountID := range want {
		if !seen[accountID] {
			t.Fatalf("expected excluded account %q in %#v", accountID, cfg.Beeper.ExcludeAccountIDs)
		}
	}
}
