package beepersource

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestBrandIconManifestCoversExpectedChatChannels(t *testing.T) {
	want := []string{
		"whatsapp", "signal", "telegram", "beeper", "matrix", "email", "imessage",
		"messenger", "instagram", "discord", "slack", "x", "linkedin",
		"creatorhero", "onlyfans", "fansly", "fanvue", "mymfans", "fancentro",
		"slushy", "uncove", "subscribestar", "maloum", "dfans", "manyvids",
		"unlockd", "sospoilt", "xpanded", "revealme", "admireme", "camsoda",
		"stacked", "fanview",
	}
	for _, key := range want {
		icon, ok := brandIconByKey(key)
		if !ok {
			t.Fatalf("missing brand icon %q", key)
		}
		if icon.BrandColor == "" || icon.PNG == "" || icon.SHA256 == "" || icon.Source == "" || icon.LicenseNote == "" {
			t.Fatalf("brand icon %q has incomplete manifest entry: %#v", key, icon)
		}
		body, err := brandIconFS.ReadFile("assets/brand-icons/" + icon.PNG)
		if err != nil {
			t.Fatalf("missing embedded PNG asset for %q: %v", key, err)
		}
		img, err := png.Decode(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("brand icon %q is not a decodable PNG: %v", key, err)
		}
		if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
			t.Fatalf("brand icon %q embedded PNG is not normalized to 256px square: %v", key, img.Bounds())
		}
	}
}

func TestBrandIconPlatformAliases(t *testing.T) {
	cases := map[string]string{
		"WhatsApp":       "whatsapp",
		"tg":             "telegram",
		"Email":          "email",
		"mail":           "email",
		"LinkedIn":       "linkedin",
		"BridgeV2":       "beeper",
		"Creator Hero":   "creatorhero",
		"Only Fans":      "onlyfans",
		"MYM.fans":       "mymfans",
		"Fan Centro":     "fancentro",
		"Subscribe Star": "subscribestar",
		"Many Vids":      "manyvids",
		"So Spoilt":      "sospoilt",
		"Reveal Me":      "revealme",
		"Admire Me":      "admireme",
		"Cam Soda":       "camsoda",
		"stacked.com":    "stacked",
	}
	for raw, want := range cases {
		if got := brandIconKey(raw); got != want {
			t.Fatalf("brandIconKey(%q) = %q, want %q", raw, got, want)
		}
	}
}

func TestFanviewBrandColorMatchesBlueAppTile(t *testing.T) {
	icon, ok := brandIconByKey("fanview")
	if !ok {
		t.Fatal("missing Fanview brand icon")
	}
	if icon.BrandColor != "#0098C7" {
		t.Fatalf("expected Fanview brand color to match blue app tile, got %q", icon.BrandColor)
	}
	if got := PlatformColor(Chat{AccountID: "fanview"}); got != "#0098C7" {
		t.Fatalf("expected Fanview platform fallback color to match blue app tile, got %q", got)
	}
}

func TestBrandIconPNGUsesLocalOverrideWhenConfigured(t *testing.T) {
	dir := t.TempDir()
	override := image.NewRGBA(image.Rect(0, 0, 4, 4))
	override.SetRGBA(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, override); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "whatsapp.png"), buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BEEPER_MATRIX_PROXY_BRAND_ICON_DIR", dir)

	body, ok := brandIconPNGByKey("whatsapp")
	if !ok {
		t.Fatal("expected override icon")
	}
	if !bytes.Equal(body, buf.Bytes()) {
		t.Fatal("expected local override icon bytes")
	}
}
