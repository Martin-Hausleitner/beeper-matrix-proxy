package beepersource

import (
	"os"
	"testing"
)

func TestDefaultConfigIsLocalBidirectionalAndSafe(t *testing.T) {
	t.Setenv("BEEPER_ACCESS_TOKEN", "token")

	cfg := DefaultConfig()

	if cfg.Beeper.BaseURL != "http://localhost:23373" {
		t.Fatalf("unexpected base URL %q", cfg.Beeper.BaseURL)
	}
	if cfg.Beeper.TokenEnv != "BEEPER_ACCESS_TOKEN" {
		t.Fatalf("unexpected token env %q", cfg.Beeper.TokenEnv)
	}
	if !cfg.Beeper.WebsocketEnabled {
		t.Fatal("expected websocket to be enabled by default")
	}
	if cfg.Sync.Mode != SyncModeBidirectional {
		t.Fatalf("expected bidirectional sync mode, got %q", cfg.Sync.Mode)
	}
	if cfg.Sync.MaxSendRPS <= 0 || cfg.Sync.MaxSendRPS > 2 {
		t.Fatalf("expected conservative send rate, got %.2f", cfg.Sync.MaxSendRPS)
	}
	if cfg.Safety.DisableMatrixToBeeper {
		t.Fatal("expected matrix->beeper to be enabled by default per requested plan")
	}
}

func TestConfigCanDisableMatrixToBeeperWithoutRedeploy(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER", "true")

	cfg := DefaultConfig()

	if !cfg.Safety.DisableMatrixToBeeper {
		t.Fatal("expected env kill switch to disable matrix->beeper")
	}
}

func TestConfigCanPreferPlatformAvatars(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS", "true")

	cfg := DefaultConfig()

	if !cfg.Matrix.PlatformAvatars {
		t.Fatal("expected platform avatars to be enabled from env")
	}
}

func TestConfigCanDisableDMParticipantAvatars(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS", "false")

	cfg := DefaultConfig()

	if cfg.Matrix.DMParticipantAvatars {
		t.Fatal("expected DM participant avatars to be disabled from env")
	}
}

func TestConfigCanDisableAvatarBadges(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES", "false")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarBadges {
		t.Fatal("expected room avatar badges to be disabled from env")
	}
}

func TestConfigCanForceAvatarSync(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_FORCE_AVATAR_SYNC", "true")

	cfg := DefaultConfig()

	if !cfg.Matrix.ForceAvatarSync {
		t.Fatal("expected avatar refresh to be enabled from env")
	}
}

func TestConfigCanEnableMatrixSpaces(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_SPACES", "true")

	cfg := DefaultConfig()

	if !cfg.Matrix.Spaces {
		t.Fatal("expected Matrix spaces to be enabled from env")
	}
}

func TestConfigCanOmitPlatformFromRoomNames(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_ROOM_INCLUDE_PLATFORM", "false")

	cfg := DefaultConfig()

	if cfg.Matrix.RoomNameIncludePlatform {
		t.Fatal("expected room names to omit platform from env")
	}
}

func TestConfigCanClearRoomNamePrefix(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX", "")

	cfg := DefaultConfig()

	if cfg.Matrix.RoomNamePrefix != "" {
		t.Fatalf("expected empty room name prefix from env, got %q", cfg.Matrix.RoomNamePrefix)
	}
}

func TestConfigCanTunePortalWorkersAndArchivedChats(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_PORTAL_WORKERS", "8")
	t.Setenv("BEEPER_MATRIX_PROXY_PORTAL_TIMEOUT_SECONDS", "25")
	t.Setenv("BEEPER_MATRIX_PROXY_INCLUDE_ARCHIVED", "true")

	cfg := DefaultConfig()

	if cfg.Sync.PortalWorkers != 8 {
		t.Fatalf("expected 8 portal workers, got %d", cfg.Sync.PortalWorkers)
	}
	if cfg.Sync.PortalTimeoutSeconds != 25 {
		t.Fatalf("expected 25s portal timeout, got %d", cfg.Sync.PortalTimeoutSeconds)
	}
	if !cfg.Sync.IncludeArchived {
		t.Fatal("expected archived chats to be included from env")
	}
}

func TestConfigCanDisablePortalAccessChecks(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_PORTAL_CHECK_ACCESS", "false")

	cfg := DefaultConfig()

	if cfg.Sync.PortalCheckAccess {
		t.Fatal("expected portal access checks to be disabled from env")
	}
}

func TestAllowsBeeperChatUsesOptionalAllowlist(t *testing.T) {
	cfg := DefaultConfig()
	if !cfg.AllowsBeeperChat("!any:beeper") {
		t.Fatal("empty allowlist should allow all chats")
	}
	cfg.Beeper.ChatIDs = []string{"!test:beeper"}
	if !cfg.AllowsBeeperChat("!test:beeper") {
		t.Fatal("expected configured chat to be allowed")
	}
	if cfg.AllowsBeeperChat("!real-contact:beeper") {
		t.Fatal("expected unlisted chat to be blocked")
	}
}

func TestAllowsBeeperChatRecordSkipsArchivedByDefault(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.AllowsBeeperChatRecord(Chat{ID: "!archived:beeper", IsArchived: true}) {
		t.Fatal("expected archived chats to be skipped by default")
	}
	cfg.Sync.IncludeArchived = true
	if !cfg.AllowsBeeperChatRecord(Chat{ID: "!archived:beeper", IsArchived: true}) {
		t.Fatal("expected archived chats to be allowed when configured")
	}
}

func TestAllowsBeeperChatRecordExcludesAccountIDs(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Beeper.ExcludeAccountIDs = []string{"beeper-matrix-proxy"}

	if cfg.AllowsBeeperChatRecord(Chat{ID: "!self:beeper", AccountID: "beeper-matrix-proxy"}) {
		t.Fatal("expected self Matrix bridge account to be excluded")
	}
	if !cfg.AllowsBeeperChatRecord(Chat{ID: "!wa:beeper", AccountID: "whatsapp"}) {
		t.Fatal("expected WhatsApp chat to be allowed")
	}
}

func TestBeeperTokenLoadsFromConfiguredEnvironment(t *testing.T) {
	const tokenEnv = "BEEPER_SOURCE_TEST_TOKEN"
	t.Setenv(tokenEnv, "secret-token")
	cfg := DefaultConfig()
	cfg.Beeper.TokenEnv = tokenEnv

	token, err := cfg.BeeperToken()

	if err != nil {
		t.Fatalf("BeeperToken returned error: %v", err)
	}
	if token != "secret-token" {
		t.Fatalf("unexpected token %q", token)
	}
}

func TestBeeperTokenExplainsMissingEnvironment(t *testing.T) {
	const tokenEnv = "BEEPER_SOURCE_MISSING_TOKEN"
	_ = os.Unsetenv(tokenEnv)
	cfg := DefaultConfig()
	cfg.Beeper.TokenEnv = tokenEnv

	if _, err := cfg.BeeperToken(); err == nil {
		t.Fatal("expected missing token to return an error")
	}
}
