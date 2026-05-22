package beepersource

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGeneratedContactAvatarUsesPersonFallbackWithMessengerBadge(t *testing.T) {
	avatar := generatedContactAvatarMedia(Chat{ID: "!chat:beeper", AccountID: "email", Network: "Email", Name: "No Photo"})
	if avatar == nil {
		t.Fatal("expected generated contact avatar")
	}
	if avatar.AssetID == platformAvatarSyncValue(Chat{Network: "Email"}) {
		t.Fatal("expected contact fallback, not service-logo avatar")
	}
	body := readMatrixMediaBytes(t, avatar)
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode generated avatar: %v", err)
	}
	if img.Bounds().Dx() != 256 || img.Bounds().Dy() != 256 {
		t.Fatalf("expected 256px avatar, got %v", img.Bounds())
	}
	if badgeWhiteRingPixels(img) > 900 {
		t.Fatalf("expected no harsh white badge ring, got %d white-ring pixels", badgeWhiteRingPixels(img))
	}
	if !badgeHasPlatformColor(img, "#5F6368") {
		t.Fatal("expected email-colored badge in bottom-right corner")
	}
}

func TestAddPlatformBadgeToAvatarUsesAppStyleBadgeWithoutHarshWhiteRing(t *testing.T) {
	original := testPNGAvatar(t, color.RGBA{R: 40, G: 90, B: 170, A: 255})
	avatar := &MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "old-hash",
		Content:     bytes.NewReader(original),
		FileName:    "person.png",
		MimeType:    "image/png",
		SizeBytes:   int64(len(original)),
	}

	badged, err := addPlatformBadgeToAvatar(Chat{AccountID: "whatsapp", Network: "WhatsApp"}, avatar)
	if err != nil {
		t.Fatalf("add badge: %v", err)
	}
	body, err := io.ReadAll(badged.Content)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	if badgeWhiteRingPixels(img) > 900 {
		t.Fatalf("expected app-style badge without a white ring, got %d white-ring pixels", badgeWhiteRingPixels(img))
	}
	if !badgeHasPlatformColor(img, "#25D366") {
		t.Fatal("expected WhatsApp-colored badge")
	}
}

func TestPortalAvatarPrefersPrivateOverrideBeforeBeeperAvatar(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	overridePath := writeTestAvatar(t, color.RGBA{R: 200, G: 20, B: 40, A: 255})
	overrideFile := writeOverrideFile(t, "beeper_chat_ids:\n      - \"!chat:beeper\"\n    avatar_file: "+quoteYAML(overridePath)+"\n")
	api := &fakeBeeperAPI{
		assets: map[string]string{"localmxc://beeper-avatar": "beeper-avatar-bytes"},
	}
	cfg := DefaultConfig()
	cfg.Matrix.ContactAvatarOverridesPath = overrideFile
	svc := NewService(cfg, store, api, &fakeMatrixSink{})

	avatar, err := svc.portalAvatar(ctx, Chat{
		ID:        "!chat:beeper",
		AccountID: "whatsapp",
		Network:   "WhatsApp",
		AvatarURL: "localmxc://beeper-avatar",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.downloadedAssets) != 0 {
		t.Fatalf("expected override to avoid Beeper avatar download, got %#v", api.downloadedAssets)
	}
	if !strings.HasPrefix(avatar.AssetID, "avatar-badge-v2:contact-override:!chat:beeper:") {
		t.Fatalf("expected override asset id, got %q", avatar.AssetID)
	}
}

func TestPortalAvatarBadgesLocalBeeperAvatarWithStableV2CacheKey(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	avatarPath := writeTestAvatar(t, color.RGBA{R: 40, G: 80, B: 180, A: 255})
	cfg := DefaultConfig()
	svc := NewService(cfg, store, &fakeBeeperAPI{}, &fakeMatrixSink{})

	avatar, err := svc.portalAvatar(ctx, Chat{
		ID:        "!chat:beeper",
		AccountID: "signal",
		Network:   "Signal",
		AvatarURL: avatarPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(avatar.AssetID, "avatar-badge-v2:"+avatarPath+":") {
		t.Fatalf("expected local avatar to receive badge v2 cache key, got %q", avatar.AssetID)
	}
}

func TestSenderAvatarPrefersPrivateOverride(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	overridePath := writeTestAvatar(t, color.RGBA{R: 20, G: 200, B: 40, A: 255})
	overrideFile := writeOverrideFile(t, "sender_ids:\n      - \"@sender:whatsapp\"\n    avatar_file: "+quoteYAML(overridePath)+"\n")
	api := &fakeBeeperAPI{
		assets: map[string]string{"localmxc://sender-avatar": "beeper-avatar-bytes"},
	}
	cfg := DefaultConfig()
	cfg.Matrix.ContactAvatarOverridesPath = overrideFile
	svc := NewService(cfg, store, api, &fakeMatrixSink{})

	avatar, err := svc.senderAvatarMedia(ctx, Sender{ID: "@sender:whatsapp", AvatarID: "localmxc://sender-avatar"})
	if err != nil {
		t.Fatal(err)
	}
	if len(api.downloadedAssets) != 0 {
		t.Fatalf("expected override to avoid Beeper sender avatar download, got %#v", api.downloadedAssets)
	}
	if avatar.AssetID != "contact-override:@sender:whatsapp" {
		t.Fatalf("expected override asset id, got %q", avatar.AssetID)
	}
}

func writeTestAvatar(t *testing.T, c color.RGBA) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "avatar.png")
	if err := os.WriteFile(path, testPNGAvatar(t, c), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeOverrideFile(t *testing.T, entry string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "contact-avatar-overrides.yaml")
	content := "contacts:\n  - display_name: Test Contact\n    " + entry
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func quoteYAML(value string) string {
	return "\"" + value + "\""
}

func badgeWhiteRingPixels(img image.Image) int {
	cx, cy := 210, 210
	outer := 40
	inner := 31
	white := 0
	for y := cy - outer; y <= cy+outer; y++ {
		for x := cx - outer; x <= cx+outer; x++ {
			dx, dy := x-cx, y-cy
			d := dx*dx + dy*dy
			if d > outer*outer || d < inner*inner || !image.Pt(x, y).In(img.Bounds()) {
				continue
			}
			r, g, b, a := img.At(x, y).RGBA()
			if a > 50000 && r > 56000 && g > 56000 && b > 56000 {
				white++
			}
		}
	}
	return white
}

func badgeHasPlatformColor(img image.Image, hex string) bool {
	want := parseHexColor(hex)
	matches := 0
	for y := 176; y < 252; y++ {
		for x := 176; x < 252; x++ {
			r16, g16, b16, a16 := img.At(x, y).RGBA()
			if a16 < 50000 {
				continue
			}
			r, g, b := int(r16>>8), int(g16>>8), int(b16>>8)
			if abs(r-int(want.R)) < 20 && abs(g-int(want.G)) < 20 && abs(b-int(want.B)) < 20 {
				matches++
			}
		}
	}
	return matches > 600
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
