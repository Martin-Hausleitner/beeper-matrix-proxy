package beepersource

import (
	"io"
	"strings"
	"testing"
)

func TestPlatformDisplayNamePrefersBeeperNetwork(t *testing.T) {
	chat := Chat{AccountID: "local-whatsapp_abc", Network: "WhatsApp"}
	if got := PlatformDisplayName(chat); got != "WhatsApp" {
		t.Fatalf("unexpected platform name %q", got)
	}
}

func TestPlatformDisplayNameFallsBackFromAccountID(t *testing.T) {
	chat := Chat{AccountID: "local-whatsapp_abc"}
	if got := PlatformDisplayName(chat); got != "WhatsApp" {
		t.Fatalf("unexpected platform fallback %q", got)
	}
}

func TestPlatformAvatarMediaUsesMessengerInitials(t *testing.T) {
	avatar := platformAvatarMedia(Chat{AccountID: "whatsapp", Network: "WhatsApp"})
	if avatar == nil {
		t.Fatal("expected generated avatar")
	}
	if avatar.MimeType != "image/png" {
		t.Fatalf("unexpected mime %q", avatar.MimeType)
	}
	if avatar.FileName != "whatsapp-bridge.png" {
		t.Fatalf("unexpected filename %q", avatar.FileName)
	}
}

func TestPlatformAvatarMediaUsesMessengerLogoForKnownServices(t *testing.T) {
	avatar := platformAvatarMedia(Chat{AccountID: "signal", Network: "Signal"})
	body := readMatrixMediaBytes(t, avatar)

	if avatar.AssetID != "platform-logo-v5:Signal" {
		t.Fatalf("expected v5 direct app icon cache key, got %q", avatar.AssetID)
	}
	if avatar.MimeType != "image/png" {
		t.Fatalf("expected Signal logo PNG, got %s", avatar.MimeType)
	}
	if len(body) < 8 || string(body[:8]) != "\x89PNG\r\n\x1a\n" {
		t.Fatalf("expected PNG signature, got %q", body[:min(8, len(body))])
	}
}

func TestPlatformAvatarMediaFallsBackToInitialsForUnknownServices(t *testing.T) {
	avatar := platformAvatarMedia(Chat{AccountID: "custom-network", Network: "Custom Network"})
	body := readMatrixMediaString(t, avatar)

	if !strings.Contains(body, "<text") || !strings.Contains(body, "CN") {
		t.Fatalf("expected initials fallback for unknown service: %s", body)
	}
}

func readMatrixMediaBytes(t *testing.T, media *MatrixMedia) []byte {
	t.Helper()
	body, err := io.ReadAll(media.Content)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func readMatrixMediaString(t *testing.T, media *MatrixMedia) string {
	t.Helper()
	body := readMatrixMediaBytes(t, media)
	return string(body)
}
