package beepersource

import (
	"crypto/sha256"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

//go:embed assets/brand-icons/manifest.json assets/brand-icons/icons/*.png
var brandIconFS embed.FS

type brandIconManifestFile struct {
	Version string      `json:"version"`
	SizePX  int         `json:"size_px"`
	Icons   []brandIcon `json:"icons"`
}

type brandIcon struct {
	Key            string `json:"key"`
	Label          string `json:"label"`
	BrandColor     string `json:"brand_color"`
	PNG            string `json:"png"`
	SHA256         string `json:"sha256"`
	Source         string `json:"source"`
	SourceKind     string `json:"source_kind"`
	SourcePriority int    `json:"source_priority"`
	LicenseNote    string `json:"license_note"`
	Fallback       bool   `json:"fallback"`
}

var (
	brandIconsOnce sync.Once
	brandIcons     map[string]brandIcon
	brandIconsErr  error
)

func brandIconByKey(key string) (brandIcon, bool) {
	icons, err := loadBrandIcons()
	if err != nil {
		return brandIcon{}, false
	}
	icon, ok := icons[brandIconKey(key)]
	return icon, ok
}

func brandIconPNGByKey(key string) ([]byte, bool) {
	icon, ok := brandIconByKey(key)
	if !ok {
		return nil, false
	}
	if body, ok := localBrandIconOverride(icon.Key); ok {
		return body, true
	}
	body, err := brandIconFS.ReadFile("assets/brand-icons/" + icon.PNG)
	if err != nil {
		return nil, false
	}
	return body, true
}

func localBrandIconOverride(key string) ([]byte, bool) {
	for _, dir := range localBrandIconOverrideDirs() {
		body, err := os.ReadFile(filepath.Join(dir, brandIconKey(key)+".png"))
		if err == nil && len(body) > 0 {
			return body, true
		}
	}
	return nil, false
}

func localBrandIconOverrideDirs() []string {
	var dirs []string
	if dir := strings.TrimSpace(os.Getenv("BEEPER_MATRIX_PROXY_BRAND_ICON_DIR")); dir != "" {
		dirs = append(dirs, dir)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		dirs = append(dirs, filepath.Join(home, "Library", "Application Support", "matrix-archive-sync", "brand-icons"))
	}
	return dirs
}

func brandIconKey(raw string) string {
	key := strings.ToLower(strings.TrimSpace(raw))
	key = strings.ReplaceAll(key, " ", "")
	key = strings.ReplaceAll(key, "-", "")
	key = strings.ReplaceAll(key, "_", "")
	switch key {
	case "wa", "whatsapp":
		return "whatsapp"
	case "tg", "telegram":
		return "telegram"
	case "signal":
		return "signal"
	case "beeper", "bridgev2", "beeper(matrix)":
		return "beeper"
	case "matrix", "matrixproxy", "beepermatrixproxy", "shvcvmmatrix", "vcvmmatrix":
		return "matrix"
	case "email", "mail", "gmail":
		return "email"
	case "imessage", "bluebubbles":
		return "imessage"
	case "facebook", "messenger", "meta":
		return "messenger"
	case "instagram", "ig":
		return "instagram"
	case "discord", "discordgo":
		return "discord"
	case "slack":
		return "slack"
	case "twitter", "x":
		return "x"
	case "linkedin":
		return "linkedin"
	case "creatorhero":
		return "creatorhero"
	case "onlyfans":
		return "onlyfans"
	case "fansly":
		return "fansly"
	case "fanvue":
		return "fanvue"
	case "mym", "mymfans", "mym.fans":
		return "mymfans"
	case "fancentro":
		return "fancentro"
	case "slushy":
		return "slushy"
	case "uncove":
		return "uncove"
	case "subscribestar":
		return "subscribestar"
	case "maloum":
		return "maloum"
	case "dfans":
		return "dfans"
	case "manyvids":
		return "manyvids"
	case "unlockd":
		return "unlockd"
	case "sospoilt":
		return "sospoilt"
	case "xpanded":
		return "xpanded"
	case "revealme":
		return "revealme"
	case "admireme":
		return "admireme"
	case "camsoda":
		return "camsoda"
	case "stacked", "stacked.com":
		return "stacked"
	case "fanview":
		return "fanview"
	default:
		return key
	}
}

func loadBrandIcons() (map[string]brandIcon, error) {
	brandIconsOnce.Do(func() {
		body, err := brandIconFS.ReadFile("assets/brand-icons/manifest.json")
		if err != nil {
			brandIconsErr = err
			return
		}
		var manifest brandIconManifestFile
		if err := json.Unmarshal(body, &manifest); err != nil {
			brandIconsErr = err
			return
		}
		brandIcons = make(map[string]brandIcon, len(manifest.Icons))
		for _, icon := range manifest.Icons {
			if icon.Key == "" || icon.PNG == "" || icon.SHA256 == "" {
				brandIconsErr = fmt.Errorf("incomplete brand icon manifest entry for %q", icon.Key)
				return
			}
			pngBytes, err := brandIconFS.ReadFile("assets/brand-icons/" + icon.PNG)
			if err != nil {
				brandIconsErr = err
				return
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(pngBytes)); got != icon.SHA256 {
				brandIconsErr = fmt.Errorf("brand icon %q hash mismatch: got %s want %s", icon.Key, got, icon.SHA256)
				return
			}
			brandIcons[brandIconKey(icon.Key)] = icon
		}
	})
	return brandIcons, brandIconsErr
}
