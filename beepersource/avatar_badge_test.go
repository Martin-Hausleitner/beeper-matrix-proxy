package beepersource

import (
	"bytes"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"testing"
)

func TestAddPlatformBadgeToAvatarOverlaysMessengerIcon(t *testing.T) {
	original := testPNGAvatar(t, color.RGBA{R: 40, G: 90, B: 170, A: 255})
	avatar := &MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "old-hash",
		Content:     bytes.NewReader(original),
		FileName:    "person.png",
		MimeType:    "image/png",
		SizeBytes:   int64(len(original)),
	}
	chat := Chat{AccountID: "whatsapp", Network: "whatsapp", Name: "Doris"}

	badged, err := addPlatformBadgeToAvatar(chat, avatar)
	if err != nil {
		t.Fatalf("add badge: %v", err)
	}

	if badged == avatar {
		t.Fatal("expected a new media value for the composed avatar")
	}
	if badged.AssetID != avatar.AssetID {
		t.Fatalf("expected asset id to stay stable, got %q", badged.AssetID)
	}
	if badged.MimeType != "image/png" {
		t.Fatalf("expected PNG output, got %q", badged.MimeType)
	}
	if badged.ContentHash == "" || badged.ContentHash == avatar.ContentHash {
		t.Fatalf("expected a fresh content hash, got %q", badged.ContentHash)
	}
	body, err := io.ReadAll(badged.Content)
	if err != nil {
		t.Fatalf("read badged avatar: %v", err)
	}
	if bytes.Equal(body, original) {
		t.Fatal("expected avatar bytes to change")
	}
	decoded, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("badged avatar should decode as png: %v", err)
	}
	if decoded.Bounds().Dx() != 256 || decoded.Bounds().Dy() != 256 {
		t.Fatalf("expected 256px square avatar, got %v", decoded.Bounds())
	}
	before := testColorAt(t, original, 230, 230)
	after := decoded.At(230, 230)
	if before == after {
		t.Fatal("expected bottom-right badge to change avatar pixels")
	}
}

func TestAddPlatformBadgeToAvatarKeepsUnsupportedInputUnchanged(t *testing.T) {
	original := []byte("not an image")
	avatar := &MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "old-hash",
		Content:     bytes.NewReader(original),
		FileName:    "person.webp",
		MimeType:    "image/webp",
		SizeBytes:   int64(len(original)),
	}

	badged, err := addPlatformBadgeToAvatar(Chat{AccountID: "whatsapp"}, avatar)
	if err != nil {
		t.Fatalf("add badge: %v", err)
	}

	if badged == avatar {
		t.Fatal("expected a replacement media reader even for unchanged input")
	}
	body, err := io.ReadAll(badged.Content)
	if err != nil {
		t.Fatalf("read fallback avatar: %v", err)
	}
	if !bytes.Equal(body, original) {
		t.Fatal("expected unsupported avatar bytes to stay unchanged")
	}
	if badged.ContentHash != avatar.ContentHash {
		t.Fatalf("expected unchanged content hash, got %q", badged.ContentHash)
	}
}

func testPNGAvatar(t *testing.T, c color.RGBA) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: c}, image.Point{}, draw.Src)
	var out bytes.Buffer
	if err := png.Encode(&out, img); err != nil {
		t.Fatalf("encode test avatar: %v", err)
	}
	return out.Bytes()
}

func testColorAt(t *testing.T, pngBytes []byte, x, y int) color.Color {
	t.Helper()
	img, err := png.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("decode test avatar: %v", err)
	}
	return img.At(x/2, y/2)
}
