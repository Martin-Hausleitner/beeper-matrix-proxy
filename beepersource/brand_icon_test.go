package beepersource

import (
	"bytes"
	"image/png"
	"testing"
)

func TestBrandIconManifestCoversExpectedChatChannels(t *testing.T) {
	want := []string{
		"whatsapp", "signal", "telegram", "beeper", "matrix", "email", "imessage",
		"messenger", "instagram", "discord", "slack", "x", "linkedin",
	}
	for _, key := range want {
		icon, ok := brandIconByKey(key)
		if !ok {
			t.Fatalf("missing brand icon %q", key)
		}
		if icon.BrandColor == "" || icon.PNG == "" || icon.SHA256 == "" || icon.Source == "" || icon.LicenseNote == "" {
			t.Fatalf("brand icon %q has incomplete manifest entry: %#v", key, icon)
		}
		body, ok := brandIconPNGByKey(key)
		if !ok {
			t.Fatalf("missing PNG asset for %q", key)
		}
		if _, err := png.Decode(bytes.NewReader(body)); err != nil {
			t.Fatalf("brand icon %q is not a decodable PNG: %v", key, err)
		}
	}
}

func TestBrandIconPlatformAliases(t *testing.T) {
	cases := map[string]string{
		"WhatsApp": "whatsapp",
		"tg":       "telegram",
		"Email":    "email",
		"mail":     "email",
		"LinkedIn": "linkedin",
		"BridgeV2": "beeper",
	}
	for raw, want := range cases {
		if got := brandIconKey(raw); got != want {
			t.Fatalf("brandIconKey(%q) = %q, want %q", raw, got, want)
		}
	}
}
