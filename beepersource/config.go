package beepersource

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	Beeper BeeperConfig
	Matrix MatrixConfig
	Sync   SyncConfig
	Media  MediaConfig
	Safety SafetyConfig
}

type BeeperConfig struct {
	BaseURL           string
	TokenEnv          string
	WebsocketEnabled  bool
	ChatIDs           []string
	ExcludeAccountIDs []string
}

type MatrixConfig struct {
	HomeserverURL              string
	TokenEnv                   string
	UserID                     string
	InviteUserID               string
	RoomNamePrefix             string
	RoomNameIncludePlatform    bool
	PrefixSender               bool
	PlatformAvatars            bool
	DMParticipantAvatars       bool
	AvatarBadges               bool
	ContactAvatarOverridesPath string
	ForceAvatarSync            bool
	Spaces                     bool
	InsecureSkipTLS            bool
}

type SyncConfig struct {
	Mode                 string
	MaxSendRPS           float64
	PortalWorkers        int
	PortalTimeoutSeconds int
	IncludeArchived      bool
	PortalCheckAccess    bool
}

type MediaConfig struct {
	MaxUploadBytes int64
}

type SafetyConfig struct {
	DisableMatrixToBeeper bool
}

func DefaultConfig() Config {
	cfg := Config{
		Beeper: BeeperConfig{
			BaseURL:           envString("BEEPER_MATRIX_PROXY_BEEPER_BASE_URL", "http://localhost:23373"),
			TokenEnv:          envString("BEEPER_MATRIX_PROXY_BEEPER_TOKEN_ENV", "BEEPER_ACCESS_TOKEN"),
			WebsocketEnabled:  envBool("BEEPER_MATRIX_PROXY_BEEPER_WEBSOCKET", true),
			ChatIDs:           envCSV("BEEPER_MATRIX_PROXY_BEEPER_CHAT_IDS"),
			ExcludeAccountIDs: envCSV("BEEPER_MATRIX_PROXY_EXCLUDE_ACCOUNT_IDS"),
		},
		Matrix: MatrixConfig{
			HomeserverURL:              envString("BEEPER_MATRIX_PROXY_MATRIX_HOMESERVER_URL", "http://localhost:8008"),
			TokenEnv:                   envString("BEEPER_MATRIX_PROXY_MATRIX_TOKEN_ENV", "MATRIX_ACCESS_TOKEN"),
			UserID:                     envString("BEEPER_MATRIX_PROXY_MATRIX_USER_ID", ""),
			InviteUserID:               envString("BEEPER_MATRIX_PROXY_MATRIX_INVITE_USER_ID", ""),
			RoomNamePrefix:             envStringAllowEmpty("BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX", "Beeper: "),
			RoomNameIncludePlatform:    envBool("BEEPER_MATRIX_PROXY_MATRIX_ROOM_INCLUDE_PLATFORM", true),
			PrefixSender:               envBool("BEEPER_MATRIX_PROXY_MATRIX_PREFIX_SENDER", true),
			PlatformAvatars:            envBool("BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS", false),
			DMParticipantAvatars:       envBool("BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS", true),
			AvatarBadges:               envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES", true),
			ContactAvatarOverridesPath: envString("BEEPER_MATRIX_PROXY_CONTACT_AVATAR_OVERRIDES", ""),
			ForceAvatarSync:            envBool("BEEPER_MATRIX_PROXY_MATRIX_FORCE_AVATAR_SYNC", false),
			Spaces:                     envBool("BEEPER_MATRIX_PROXY_MATRIX_SPACES", false),
			InsecureSkipTLS:            envBool("BEEPER_MATRIX_PROXY_MATRIX_INSECURE_TLS", false),
		},
		Sync: SyncConfig{
			Mode:                 envString("BEEPER_MATRIX_PROXY_SYNC_MODE", SyncModeBidirectional),
			MaxSendRPS:           envFloat("BEEPER_MATRIX_PROXY_MAX_SEND_RPS", 1.0),
			PortalWorkers:        envInt("BEEPER_MATRIX_PROXY_PORTAL_WORKERS", 1),
			PortalTimeoutSeconds: envInt("BEEPER_MATRIX_PROXY_PORTAL_TIMEOUT_SECONDS", 75),
			IncludeArchived:      envBool("BEEPER_MATRIX_PROXY_INCLUDE_ARCHIVED", false),
			PortalCheckAccess:    envBool("BEEPER_MATRIX_PROXY_PORTAL_CHECK_ACCESS", true),
		},
		Media: MediaConfig{
			MaxUploadBytes: envInt64("BEEPER_MATRIX_PROXY_MEDIA_MAX_UPLOAD_BYTES", 0),
		},
		Safety: SafetyConfig{
			DisableMatrixToBeeper: envBool("BEEPER_MATRIX_PROXY_DISABLE_MATRIX_TO_BEEPER", false),
		},
	}
	if cfg.Sync.Mode == "" {
		cfg.Sync.Mode = SyncModeBidirectional
	}
	if cfg.Sync.MaxSendRPS <= 0 {
		cfg.Sync.MaxSendRPS = 1.0
	}
	if cfg.Sync.PortalWorkers <= 0 {
		cfg.Sync.PortalWorkers = 1
	}
	if cfg.Sync.PortalTimeoutSeconds <= 0 {
		cfg.Sync.PortalTimeoutSeconds = 75
	}
	return cfg
}

func (c Config) BeeperToken() (string, error) {
	envName := c.Beeper.TokenEnv
	if envName == "" {
		envName = "BEEPER_ACCESS_TOKEN"
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if token == "" {
		return "", fmt.Errorf("missing Beeper Desktop API token in %s", envName)
	}
	return token, nil
}

func (c Config) AllowsBeeperChat(chatID string) bool {
	if len(c.Beeper.ChatIDs) == 0 {
		return true
	}
	for _, allowed := range c.Beeper.ChatIDs {
		if allowed == chatID {
			return true
		}
	}
	return false
}

func (c Config) AllowsBeeperChatRecord(chat Chat) bool {
	for _, accountID := range c.Beeper.ExcludeAccountIDs {
		if strings.EqualFold(strings.TrimSpace(accountID), strings.TrimSpace(chat.AccountID)) {
			return false
		}
	}
	if chat.IsArchived && !c.Sync.IncludeArchived {
		return false
	}
	return c.AllowsBeeperChat(chat.ID)
}

func (c Config) MatrixToken() (string, error) {
	envName := c.Matrix.TokenEnv
	if envName == "" {
		envName = "MATRIX_ACCESS_TOKEN"
	}
	token := strings.TrimSpace(os.Getenv(envName))
	if token == "" {
		return "", fmt.Errorf("missing Matrix access token in %s", envName)
	}
	return token, nil
}

func envString(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envStringAllowEmpty(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func envCSV(key string) []string {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			out = append(out, value)
		}
	}
	return out
}

func envBool(key string, fallback bool) bool {
	value := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if value == "" {
		return fallback
	}
	switch value {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return fallback
	}
}

func envFloat(key string, fallback float64) float64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseFloat(value, 64)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt(key string, fallback int) int {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func envInt64(key string, fallback int64) int64 {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return fallback
	}
	return parsed
}
