package beepersource

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/draw"
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
	base := image.NewRGBA(image.Rect(0, 0, 256, 256))
	drawContactFallbackBase(base, Chat{ID: "!chat:beeper", AccountID: "email", Network: "Email", Name: "No Photo"})
	if badgeWhiteRingPixels(img)-badgeWhiteRingPixels(base) > 250 {
		t.Fatalf("expected no harsh white badge ring, got %d excess white-ring pixels", badgeWhiteRingPixels(img)-badgeWhiteRingPixels(base))
	}
	if badgeDeltaPixels(base, img) < 1000 {
		t.Fatal("expected a visible messenger badge in the bottom-right corner")
	}
}

func TestPortalAvatarWithoutSourcePhotoUsesInitialsFallbackWithMessengerBadge(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	svc := NewService(DefaultConfig(), store, &fakeBeeperAPI{}, &fakeMatrixSink{})

	chat := Chat{
		ID:        "!no-photo:beeper",
		AccountID: "signal",
		Network:   "Signal",
		Name:      "No Photo Person",
		Participants: []Sender{
			{ID: "@no-photo:signal", DisplayName: "No Photo Person"},
		},
	}
	avatar, err := svc.portalAvatar(ctx, chat)
	if err != nil {
		t.Fatal(err)
	}
	if avatar == nil {
		t.Fatal("expected generated initials avatar")
	}
	if !strings.HasPrefix(avatar.AssetID, "avatar-fallback-v18:!no-photo:beeper:single:signal:") {
		t.Fatalf("expected v18 initials fallback asset id, got %q", avatar.AssetID)
	}
	body := readMatrixMediaBytes(t, avatar)
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode generated avatar: %v", err)
	}
	if whiteInitialPixels(img) < 1500 {
		t.Fatalf("expected visible white initials, got %d white pixels", whiteInitialPixels(img))
	}
	base := image.NewRGBA(image.Rect(0, 0, 256, 256))
	drawContactFallbackBase(base, chat)
	if badgeDeltaPixels(base, img) < 1000 {
		t.Fatal("expected the Signal badge to be composed into the generated initials avatar")
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
	base := image.NewRGBA(image.Rect(0, 0, 256, 256))
	draw.Draw(base, base.Bounds(), &image.Uniform{C: color.RGBA{R: 40, G: 90, B: 170, A: 255}}, image.Point{}, draw.Src)
	if badgeWhiteRingPixels(img)-badgeWhiteRingPixels(base) > 250 {
		t.Fatalf("expected app-style badge without a white ring, got %d excess white-ring pixels", badgeWhiteRingPixels(img)-badgeWhiteRingPixels(base))
	}
	if badgeDeltaPixels(base, img) < 1000 {
		t.Fatal("expected visible WhatsApp badge pixels")
	}
}

func TestGeneratedContactAvatarUsesDistinctGradientAndCenteredInitials(t *testing.T) {
	alice := image.NewRGBA(image.Rect(0, 0, 256, 256))
	bob := image.NewRGBA(image.Rect(0, 0, 256, 256))
	drawContactFallbackBase(alice, Chat{ID: "!alice:beeper", Name: "Alice Example"})
	drawContactFallbackBase(bob, Chat{ID: "!bob:beeper", Name: "Bob Example"})

	if sampledColorDistance(alice, bob, 128, 30) < 35 {
		t.Fatal("expected distinct generated background colors for different contacts")
	}
	if sampledColorDistance(alice, alice, 128, 30) != 0 {
		t.Fatal("expected deterministic color samples")
	}
	if gradientDistance(alice) < 20 {
		t.Fatal("expected a visible but subtle gradient, not a flat color")
	}
	if whiteInitialPixels(alice) < 1500 {
		t.Fatalf("expected centered white initials, got %d white pixels", whiteInitialPixels(alice))
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
	if !strings.HasPrefix(avatar.AssetID, "avatar-badge-v9:contact-override:!chat:beeper:") {
		t.Fatalf("expected override asset id, got %q", avatar.AssetID)
	}
}

func sampledColorDistance(a image.Image, b image.Image, x int, y int) int {
	ar, ag, ab, _ := a.At(x, y).RGBA()
	br, bg, bb, _ := b.At(x, y).RGBA()
	return abs(int(ar>>8)-int(br>>8)) + abs(int(ag>>8)-int(bg>>8)) + abs(int(ab>>8)-int(bb>>8))
}

func gradientDistance(img image.Image) int {
	return sampledColorDistance(img, img, 128, 30) + sampledColorDistanceAt(img, 128, 30, 128, 220)
}

func sampledColorDistanceAt(img image.Image, x1 int, y1 int, x2 int, y2 int) int {
	r1, g1, b1, _ := img.At(x1, y1).RGBA()
	r2, g2, b2, _ := img.At(x2, y2).RGBA()
	return abs(int(r1>>8)-int(r2>>8)) + abs(int(g1>>8)-int(g2>>8)) + abs(int(b1>>8)-int(b2>>8))
}

func whiteInitialPixels(img image.Image) int {
	count := 0
	for y := 60; y < 160; y++ {
		for x := 45; x < 211; x++ {
			r, g, b, a := img.At(x, y).RGBA()
			if a > 50000 && r > 56000 && g > 56000 && b > 56000 {
				count++
			}
		}
	}
	return count
}

func TestPortalAvatarBadgesLocalBeeperAvatarWithStableV8CacheKey(t *testing.T) {
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
	if !strings.HasPrefix(avatar.AssetID, "avatar-badge-v9:"+avatarPath+":") {
		t.Fatalf("expected local avatar to receive badge v9 cache key, got %q", avatar.AssetID)
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
	white := 0
	for y := 150; y < 226; y++ {
		for x := 150; x < 226; x++ {
			xx, yy := x-150, y-150
			if !inRoundedRect(xx, yy, 76, 76, 19) || inRoundedRect(xx-4, yy-4, 68, 68, 16) {
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

func badgeDeltaPixels(before image.Image, after image.Image) int {
	changed := 0
	for y := 150; y < 226; y++ {
		for x := 150; x < 226; x++ {
			r1, g1, b1, a1 := before.At(x, y).RGBA()
			r2, g2, b2, a2 := after.At(x, y).RGBA()
			if abs(int(r1>>8)-int(r2>>8))+abs(int(g1>>8)-int(g2>>8))+abs(int(b1>>8)-int(b2>>8))+abs(int(a1>>8)-int(a2>>8)) > 24 {
				changed++
			}
		}
	}
	return changed
}

func abs(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
