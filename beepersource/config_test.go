package beepersource

import (
	"os"
	"path/filepath"
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
	if cfg.Matrix.RoomNamePrefix != "" {
		t.Fatalf("expected plain room names by default, got prefix %q", cfg.Matrix.RoomNamePrefix)
	}
	if cfg.Matrix.RoomNameIncludePlatform {
		t.Fatal("expected room names to omit platform labels by default")
	}
	if cfg.Matrix.AvatarClientProfile != "cinny" || cfg.Matrix.AvatarBadgeLayout != "edge" || cfg.Matrix.AvatarBadgeSizePercent != 28 || cfg.Matrix.AvatarBadgeInsetPercent != 0 || cfg.Matrix.GroupAvatarMaxParticipants != 10 {
		t.Fatalf("expected Cinny avatar profile defaults, got %#v", cfg.Matrix)
	}
}

func TestConfigCanDisableMatrixToBeeperWithoutRedeploy(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER", "true")

	cfg := DefaultConfig()

	if !cfg.Safety.DisableMatrixToBeeper {
		t.Fatal("expected env kill switch to disable matrix->beeper")
	}
}

func TestConfigVoiceAIDefaultsToDisabledAndFailClosed(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.VoiceAI.Enabled {
		t.Fatal("expected voice AI to be disabled by default")
	}
	if len(cfg.VoiceAI.AllowChatIDs)+len(cfg.VoiceAI.AllowChatNames)+len(cfg.VoiceAI.AllowSenderIDs)+len(cfg.VoiceAI.AllowSenderNames) != 0 {
		t.Fatalf("expected voice AI identity allowlists to default empty, got %#v", cfg.VoiceAI)
	}
	if cfg.VoiceAI.SummaryBaseURL != "http://127.0.0.1:1234/v1" || cfg.VoiceAI.Language != "auto" {
		t.Fatalf("unexpected voice AI local defaults: %#v", cfg.VoiceAI)
	}
}

func TestConfigVoiceAICanBeEnabledForSpecificSignalAndWhatsAppChats(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_VOICE_AI_ENABLED", "true")
	t.Setenv("BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_NETWORKS", "Signal,WhatsApp")
	t.Setenv("BEEPER_MATRIX_PROXY_VOICE_AI_ALLOW_CHAT_NAMES", "Felix Ratzenberg,Signal Test,WhatsApp Test")
	t.Setenv("BEEPER_MATRIX_PROXY_VOICE_AI_TRANSCRIBE_COMMAND", "uvx --from mlx-whisper mlx_whisper \"$AUDIO_PATH\" --model mlx-community/whisper-large-v3-turbo")
	t.Setenv("BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_MODEL", "qwen3-local")

	cfg := DefaultConfig()

	if !cfg.VoiceAI.Enabled {
		t.Fatal("expected voice AI to be enabled from env")
	}
	if len(cfg.VoiceAI.AllowNetworks) != 2 || cfg.VoiceAI.AllowNetworks[0] != "Signal" {
		t.Fatalf("unexpected voice AI networks: %#v", cfg.VoiceAI.AllowNetworks)
	}
	if len(cfg.VoiceAI.AllowChatNames) != 3 || cfg.VoiceAI.AllowChatNames[0] != "Felix Ratzenberg" {
		t.Fatalf("unexpected voice AI chat names: %#v", cfg.VoiceAI.AllowChatNames)
	}
	if cfg.VoiceAI.TranscribeCommand == "" || cfg.VoiceAI.SummaryModel != "qwen3-local" {
		t.Fatalf("unexpected voice AI command/model config: %#v", cfg.VoiceAI)
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
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_FALLBACK_BADGES", "false")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarBadges {
		t.Fatal("expected room avatar badges to be disabled from env")
	}
	if cfg.Matrix.AvatarFallbackBadges {
		t.Fatal("expected generated fallback avatar badges to be disabled from env")
	}
}

func TestConfigCanUseBeeperNativeAvatarProfile(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", "bipa-native")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarClientProfile != "beeper-native" {
		t.Fatalf("expected native Beeper/BIPA profile, got %q", cfg.Matrix.AvatarClientProfile)
	}
	if cfg.Matrix.AvatarBadges || cfg.Matrix.AvatarFallbackBadges || cfg.Matrix.PlatformAvatars {
		t.Fatalf("expected native profile to avoid avatar rewriting, got %#v", cfg.Matrix)
	}
	if !cfg.Matrix.DMParticipantAvatars || cfg.Matrix.GroupAvatarStyle != "initials" || cfg.Matrix.GroupAvatarMaxParticipants != 2 || cfg.Matrix.GroupAvatarOverlapPercent != 0 || cfg.Matrix.AvatarBadgeInsetPercent != 3 {
		t.Fatalf("expected native profile to keep source photos and simple fallbacks, got %#v", cfg.Matrix)
	}
}

func TestConfigExplicitAvatarSourceEnvOverridesNativeProfile(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", "beeper-native")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS", "true")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS", "false")

	cfg := DefaultConfig()

	if !cfg.Matrix.PlatformAvatars || cfg.Matrix.DMParticipantAvatars {
		t.Fatalf("expected explicit avatar source env values to override native profile, got %#v", cfg.Matrix)
	}
}

func TestConfigAvatarProfileCanBeOverriddenByExplicitEnv(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", "element")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SIZE_PERCENT", "31")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_MAX_PARTICIPANTS", "5")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_EXCLUDE_SELF", "false")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarClientProfile != "element" {
		t.Fatalf("expected element profile, got %q", cfg.Matrix.AvatarClientProfile)
	}
	if cfg.Matrix.AvatarBadgeSizePercent != 31 || cfg.Matrix.GroupAvatarMaxParticipants != 5 || cfg.Matrix.GroupAvatarExcludeSelf {
		t.Fatalf("expected explicit env to override profile defaults, got %#v", cfg.Matrix)
	}
}

func TestConfigEmptyAvatarProfileEnvDoesNotOverrideConfigFile(t *testing.T) {
	path := writeTextFile(t, "avatar-badge.yaml", []byte(`
avatar_badge:
  client_profile: element
`))
	t.Setenv("BEEPER_MATRIX_PROXY_AVATAR_BADGE_CONFIG", path)
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", "")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarClientProfile != "element" || cfg.Matrix.AvatarBadgeSizePercent != 26 || cfg.Matrix.AvatarBadgeInsetPercent != 4 {
		t.Fatalf("expected empty profile env to leave file profile intact, got %#v", cfg.Matrix)
	}
}

func TestConfigCanTuneAvatarBadgeFromEnv(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_POSITION", "bottom-left")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_LAYOUT", "circle-safe")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHAPE", "circle")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SIZE_PERCENT", "34")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_INSET_PERCENT", "2")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHADOW", "false")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_STYLE", "bubbles")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_MAX_PARTICIPANTS", "5")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_OVERLAP_PERCENT", "28")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_EXCLUDE_SELF", "false")
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_SELF_IDS", "me@example,@me:matrix.test")

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarBadgePosition != "bottom-left" || cfg.Matrix.AvatarBadgeLayout != "circle-safe" || cfg.Matrix.AvatarBadgeShape != "circle" {
		t.Fatalf("unexpected badge config: %#v", cfg.Matrix)
	}
	if cfg.Matrix.AvatarBadgeSizePercent != 34 || cfg.Matrix.AvatarBadgeInsetPercent != 2 || cfg.Matrix.AvatarBadgeShadow {
		t.Fatalf("unexpected badge sizing/shadow config: %#v", cfg.Matrix)
	}
	if cfg.Matrix.GroupAvatarStyle != "bubbles" || cfg.Matrix.GroupAvatarMaxParticipants != 5 || cfg.Matrix.GroupAvatarOverlapPercent != 28 || cfg.Matrix.GroupAvatarExcludeSelf {
		t.Fatalf("unexpected group avatar config: %#v", cfg.Matrix)
	}
	if len(cfg.Matrix.GroupAvatarSelfIDs) != 2 || cfg.Matrix.GroupAvatarSelfIDs[0] != "me@example" {
		t.Fatalf("unexpected group avatar self ids: %#v", cfg.Matrix.GroupAvatarSelfIDs)
	}
}

func TestConfigCanLoadAvatarBadgeConfigFile(t *testing.T) {
	path := writeTextFile(t, "avatar-badge.yaml", []byte(`
avatar_badge:
  client_profile: element
  fallback_badges: false
  position: top-right
  layout: edge
  shape: rounded
  size_percent: 29
  inset_percent: 4
  shadow: false
group_avatar:
  style: bubbles
  max_participants: 5
  overlap_percent: 26
  exclude_self: true
  self_ids:
    - my-beeper-id
`))
	t.Setenv("BEEPER_MATRIX_PROXY_AVATAR_BADGE_CONFIG", path)

	cfg := DefaultConfig()

	if cfg.Matrix.AvatarClientProfile != "element" || cfg.Matrix.AvatarFallbackBadges {
		t.Fatalf("unexpected client profile from file: %#v", cfg.Matrix)
	}
	if cfg.Matrix.AvatarBadgePosition != "top-right" || cfg.Matrix.AvatarBadgeLayout != "edge" || cfg.Matrix.AvatarBadgeShape != "rounded" {
		t.Fatalf("unexpected badge config from file: %#v", cfg.Matrix)
	}
	if cfg.Matrix.AvatarBadgeSizePercent != 29 || cfg.Matrix.AvatarBadgeInsetPercent != 4 || cfg.Matrix.AvatarBadgeShadow {
		t.Fatalf("unexpected badge sizing/shadow config from file: %#v", cfg.Matrix)
	}
	if cfg.Matrix.GroupAvatarStyle != "bubbles" || cfg.Matrix.GroupAvatarMaxParticipants != 5 || cfg.Matrix.GroupAvatarOverlapPercent != 26 || !cfg.Matrix.GroupAvatarExcludeSelf {
		t.Fatalf("unexpected group avatar config from file: %#v", cfg.Matrix)
	}
	if len(cfg.Matrix.GroupAvatarSelfIDs) != 1 || cfg.Matrix.GroupAvatarSelfIDs[0] != "my-beeper-id" {
		t.Fatalf("unexpected group avatar self ids from file: %#v", cfg.Matrix.GroupAvatarSelfIDs)
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

func TestConfigCanUseTeamStyleSpaceGrouping(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_SPACE_GROUPING", "platform-account")

	cfg := DefaultConfig()

	if cfg.Matrix.SpaceGrouping != "platform-account" {
		t.Fatalf("expected platform-account space grouping from env, got %q", cfg.Matrix.SpaceGrouping)
	}
}

func TestConfigCanIncludePlatformInRoomNames(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_MATRIX_ROOM_INCLUDE_PLATFORM", "true")

	cfg := DefaultConfig()

	if !cfg.Matrix.RoomNameIncludePlatform {
		t.Fatal("expected room names to include platform from env")
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

func writeTextFile(t *testing.T, name string, body []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
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
