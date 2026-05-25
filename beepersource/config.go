package beepersource

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
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
	AvatarClientProfile        string
	AvatarBadges               bool
	AvatarFallbackBadges       bool
	AvatarBadgeConfigPath      string
	AvatarBadgePosition        string
	AvatarBadgeLayout          string
	AvatarBadgeShape           string
	AvatarBadgeSizePercent     int
	AvatarBadgeInsetPercent    int
	AvatarBadgeShadow          bool
	GroupAvatarStyle           string
	GroupAvatarMaxParticipants int
	GroupAvatarOverlapPercent  int
	GroupAvatarExcludeSelf     bool
	GroupAvatarSelfIDs         []string
	ContactAvatarOverridesPath string
	ForceAvatarSync            bool
	Spaces                     bool
	SpaceGrouping              string
	InsecureSkipTLS            bool
}

type avatarBadgeConfigFile struct {
	AvatarBadge struct {
		ClientProfile  string `yaml:"client_profile"`
		FallbackBadges *bool  `yaml:"fallback_badges"`
		Position       string `yaml:"position"`
		Layout         string `yaml:"layout"`
		Shape          string `yaml:"shape"`
		SizePercent    *int   `yaml:"size_percent"`
		InsetPercent   *int   `yaml:"inset_percent"`
		Shadow         *bool  `yaml:"shadow"`
	} `yaml:"avatar_badge"`
	GroupAvatar struct {
		Style           string   `yaml:"style"`
		MaxParticipants *int     `yaml:"max_participants"`
		OverlapPercent  *int     `yaml:"overlap_percent"`
		ExcludeSelf     *bool    `yaml:"exclude_self"`
		SelfIDs         []string `yaml:"self_ids"`
	} `yaml:"group_avatar"`
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
			RoomNamePrefix:             envStringAllowEmpty("BEEPER_MATRIX_PROXY_MATRIX_ROOM_PREFIX", ""),
			RoomNameIncludePlatform:    envBool("BEEPER_MATRIX_PROXY_MATRIX_ROOM_INCLUDE_PLATFORM", false),
			PrefixSender:               envBool("BEEPER_MATRIX_PROXY_MATRIX_PREFIX_SENDER", true),
			PlatformAvatars:            envBool("BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS", false),
			DMParticipantAvatars:       envBool("BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS", true),
			AvatarClientProfile:        envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", "cinny"),
			AvatarBadges:               envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES", true),
			AvatarFallbackBadges:       envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_FALLBACK_BADGES", true),
			AvatarBadgeConfigPath:      envString("BEEPER_MATRIX_PROXY_AVATAR_BADGE_CONFIG", ""),
			AvatarBadgePosition:        envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_POSITION", "bottom-right"),
			AvatarBadgeLayout:          envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_LAYOUT", "circle-safe"),
			AvatarBadgeShape:           envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHAPE", "rounded"),
			AvatarBadgeSizePercent:     envInt("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SIZE_PERCENT", 34),
			AvatarBadgeInsetPercent:    envInt("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_INSET_PERCENT", 0),
			AvatarBadgeShadow:          envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHADOW", true),
			GroupAvatarStyle:           envString("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_STYLE", "auto"),
			GroupAvatarMaxParticipants: envInt("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_MAX_PARTICIPANTS", 4),
			GroupAvatarOverlapPercent:  envInt("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_OVERLAP_PERCENT", 34),
			GroupAvatarExcludeSelf:     envBool("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_EXCLUDE_SELF", true),
			GroupAvatarSelfIDs:         envCSV("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_SELF_IDS"),
			ContactAvatarOverridesPath: envString("BEEPER_MATRIX_PROXY_CONTACT_AVATAR_OVERRIDES", ""),
			ForceAvatarSync:            envBool("BEEPER_MATRIX_PROXY_MATRIX_FORCE_AVATAR_SYNC", false),
			Spaces:                     envBool("BEEPER_MATRIX_PROXY_MATRIX_SPACES", false),
			SpaceGrouping:              envString("BEEPER_MATRIX_PROXY_MATRIX_SPACE_GROUPING", "platform"),
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
	applyAvatarClientProfile(&cfg)
	applyAvatarBadgeConfigFile(&cfg)
	if profile := envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_CLIENT_PROFILE", ""); profile != "" {
		cfg.Matrix.AvatarClientProfile = profile
		applyAvatarClientProfile(&cfg)
	}
	cfg.Matrix.PlatformAvatars = envBool("BEEPER_MATRIX_PROXY_MATRIX_PLATFORM_AVATARS", cfg.Matrix.PlatformAvatars)
	cfg.Matrix.DMParticipantAvatars = envBool("BEEPER_MATRIX_PROXY_MATRIX_DM_PARTICIPANT_AVATARS", cfg.Matrix.DMParticipantAvatars)
	cfg.Matrix.AvatarBadges = envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGES", cfg.Matrix.AvatarBadges)
	cfg.Matrix.AvatarFallbackBadges = envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_FALLBACK_BADGES", cfg.Matrix.AvatarFallbackBadges)
	cfg.Matrix.AvatarBadgePosition = envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_POSITION", cfg.Matrix.AvatarBadgePosition)
	cfg.Matrix.AvatarBadgeLayout = envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_LAYOUT", cfg.Matrix.AvatarBadgeLayout)
	cfg.Matrix.AvatarBadgeShape = envString("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHAPE", cfg.Matrix.AvatarBadgeShape)
	cfg.Matrix.AvatarBadgeSizePercent = envInt("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SIZE_PERCENT", cfg.Matrix.AvatarBadgeSizePercent)
	cfg.Matrix.AvatarBadgeInsetPercent = envInt("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_INSET_PERCENT", cfg.Matrix.AvatarBadgeInsetPercent)
	cfg.Matrix.AvatarBadgeShadow = envBool("BEEPER_MATRIX_PROXY_MATRIX_AVATAR_BADGE_SHADOW", cfg.Matrix.AvatarBadgeShadow)
	cfg.Matrix.GroupAvatarStyle = envString("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_STYLE", cfg.Matrix.GroupAvatarStyle)
	cfg.Matrix.GroupAvatarMaxParticipants = envInt("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_MAX_PARTICIPANTS", cfg.Matrix.GroupAvatarMaxParticipants)
	cfg.Matrix.GroupAvatarOverlapPercent = envInt("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_OVERLAP_PERCENT", cfg.Matrix.GroupAvatarOverlapPercent)
	cfg.Matrix.GroupAvatarExcludeSelf = envBool("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_EXCLUDE_SELF", cfg.Matrix.GroupAvatarExcludeSelf)
	if selfIDs := envCSV("BEEPER_MATRIX_PROXY_MATRIX_GROUP_AVATAR_SELF_IDS"); len(selfIDs) > 0 {
		cfg.Matrix.GroupAvatarSelfIDs = selfIDs
	}
	return cfg
}

func applyAvatarClientProfile(cfg *Config) {
	profile := normalizeAvatarClientProfile(cfg.Matrix.AvatarClientProfile)
	cfg.Matrix.AvatarClientProfile = profile
	switch profile {
	case "cinny":
		cfg.Matrix.AvatarBadges = true
		cfg.Matrix.AvatarFallbackBadges = true
		cfg.Matrix.AvatarBadgeLayout = "circle-safe"
		cfg.Matrix.AvatarBadgeShape = "rounded"
		cfg.Matrix.AvatarBadgeSizePercent = 22
		cfg.Matrix.AvatarBadgeInsetPercent = 0
		cfg.Matrix.AvatarBadgeShadow = false
		cfg.Matrix.GroupAvatarStyle = "auto"
		cfg.Matrix.GroupAvatarMaxParticipants = 10
		cfg.Matrix.GroupAvatarOverlapPercent = 34
	case "element":
		cfg.Matrix.AvatarBadges = true
		cfg.Matrix.AvatarFallbackBadges = true
		cfg.Matrix.AvatarBadgeLayout = "circle-safe"
		cfg.Matrix.AvatarBadgeShape = "rounded"
		cfg.Matrix.AvatarBadgeSizePercent = 24
		cfg.Matrix.AvatarBadgeInsetPercent = 0
		cfg.Matrix.AvatarBadgeShadow = false
		cfg.Matrix.GroupAvatarStyle = "auto"
		cfg.Matrix.GroupAvatarMaxParticipants = 10
		cfg.Matrix.GroupAvatarOverlapPercent = 32
	case "generic":
		cfg.Matrix.AvatarBadges = true
		cfg.Matrix.AvatarFallbackBadges = true
		cfg.Matrix.AvatarBadgeLayout = "circle-safe"
		cfg.Matrix.AvatarBadgeShape = "rounded"
		cfg.Matrix.AvatarBadgeSizePercent = 26
		cfg.Matrix.AvatarBadgeInsetPercent = 0
		cfg.Matrix.AvatarBadgeShadow = true
		cfg.Matrix.GroupAvatarStyle = "auto"
		cfg.Matrix.GroupAvatarMaxParticipants = 6
		cfg.Matrix.GroupAvatarOverlapPercent = 30
	case "beeper-native":
		cfg.Matrix.AvatarBadges = false
		cfg.Matrix.AvatarFallbackBadges = false
		cfg.Matrix.PlatformAvatars = false
		cfg.Matrix.DMParticipantAvatars = true
		cfg.Matrix.AvatarBadgeLayout = "circle-safe"
		cfg.Matrix.AvatarBadgeShape = "rounded"
		cfg.Matrix.AvatarBadgeSizePercent = 22
		cfg.Matrix.AvatarBadgeInsetPercent = 0
		cfg.Matrix.AvatarBadgeShadow = false
		cfg.Matrix.GroupAvatarStyle = "initials"
		cfg.Matrix.GroupAvatarMaxParticipants = 2
		cfg.Matrix.GroupAvatarOverlapPercent = 0
	case "custom":
		return
	}
	cfg.Matrix.GroupAvatarExcludeSelf = true
}

func normalizeAvatarClientProfile(profile string) string {
	switch strings.ToLower(strings.TrimSpace(profile)) {
	case "", "cinny", "cinny-web":
		return "cinny"
	case "element", "element-web", "element-desktop":
		return "element"
	case "generic", "matrix", "matrix-standard", "standard":
		return "generic"
	case "beeper", "bipa", "beeper-native", "bipa-native", "native":
		return "beeper-native"
	case "custom", "manual":
		return "custom"
	default:
		return "cinny"
	}
}

func applyAvatarBadgeConfigFile(cfg *Config) {
	path := strings.TrimSpace(cfg.Matrix.AvatarBadgeConfigPath)
	if path == "" {
		return
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var file avatarBadgeConfigFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return
	}
	if file.AvatarBadge.ClientProfile != "" {
		cfg.Matrix.AvatarClientProfile = file.AvatarBadge.ClientProfile
		applyAvatarClientProfile(cfg)
	}
	if file.AvatarBadge.FallbackBadges != nil {
		cfg.Matrix.AvatarFallbackBadges = *file.AvatarBadge.FallbackBadges
	}
	if file.AvatarBadge.Position != "" {
		cfg.Matrix.AvatarBadgePosition = file.AvatarBadge.Position
	}
	if file.AvatarBadge.Layout != "" {
		cfg.Matrix.AvatarBadgeLayout = file.AvatarBadge.Layout
	}
	if file.AvatarBadge.Shape != "" {
		cfg.Matrix.AvatarBadgeShape = file.AvatarBadge.Shape
	}
	if file.AvatarBadge.SizePercent != nil && *file.AvatarBadge.SizePercent > 0 {
		cfg.Matrix.AvatarBadgeSizePercent = *file.AvatarBadge.SizePercent
	}
	if file.AvatarBadge.InsetPercent != nil && *file.AvatarBadge.InsetPercent >= 0 {
		cfg.Matrix.AvatarBadgeInsetPercent = *file.AvatarBadge.InsetPercent
	}
	if file.AvatarBadge.Shadow != nil {
		cfg.Matrix.AvatarBadgeShadow = *file.AvatarBadge.Shadow
	}
	if file.GroupAvatar.Style != "" {
		cfg.Matrix.GroupAvatarStyle = file.GroupAvatar.Style
	}
	if file.GroupAvatar.MaxParticipants != nil && *file.GroupAvatar.MaxParticipants > 0 {
		cfg.Matrix.GroupAvatarMaxParticipants = *file.GroupAvatar.MaxParticipants
	}
	if file.GroupAvatar.OverlapPercent != nil && *file.GroupAvatar.OverlapPercent >= 0 {
		cfg.Matrix.GroupAvatarOverlapPercent = *file.GroupAvatar.OverlapPercent
	}
	if file.GroupAvatar.ExcludeSelf != nil {
		cfg.Matrix.GroupAvatarExcludeSelf = *file.GroupAvatar.ExcludeSelf
	}
	if len(file.GroupAvatar.SelfIDs) > 0 {
		cfg.Matrix.GroupAvatarSelfIDs = file.GroupAvatar.SelfIDs
	}
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
