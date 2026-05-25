package beepersource

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"io"
	"math"
	"strings"
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
	if !strings.HasPrefix(badged.AssetID, "avatar-badge-v9:"+avatar.AssetID+":") {
		t.Fatalf("expected v9 badge asset id, got %q", badged.AssetID)
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
	x, y, size := avatarBadgeRect(defaultAvatarBadgeOptions())
	sampleX := x + size/2
	sampleY := y + size/2
	before := testColorAt(t, original, sampleX, sampleY)
	after := decoded.At(sampleX, sampleY)
	if before == after {
		t.Fatal("expected badge to change avatar pixels")
	}
}

func TestAvatarBadgeOptionsCanMoveBadgeToBottomLeft(t *testing.T) {
	original := testPNGAvatar(t, color.RGBA{R: 40, G: 90, B: 170, A: 255})
	avatar := &MatrixMedia{
		AssetID:  "avatar-asset",
		Content:  bytes.NewReader(original),
		FileName: "person.png",
		MimeType: "image/png",
	}
	opts := defaultAvatarBadgeOptions()
	opts.position = "bottom-left"
	opts.layout = "edge"
	badged, err := addPlatformBadgeToAvatarWithOptions(Chat{AccountID: "signal", Network: "Signal"}, avatar, opts)
	if err != nil {
		t.Fatalf("add badge: %v", err)
	}
	body, err := io.ReadAll(badged.Content)
	if err != nil {
		t.Fatalf("read badged avatar: %v", err)
	}
	decoded, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode badged avatar: %v", err)
	}
	if decoded.At(35, 220) == testColorAt(t, original, 35, 220) {
		t.Fatal("expected bottom-left badge placement to change lower-left pixels")
	}
	if decoded.At(220, 220) != testColorAt(t, original, 220, 220) {
		t.Fatal("did not expect bottom-right pixels to contain the badge")
	}
}

func TestAvatarBadgeEdgeLayoutSitsCloserToCornerThanCircleSafe(t *testing.T) {
	edge := defaultAvatarBadgeOptions()
	edge.layout = "edge"
	edgeX, edgeY, _ := avatarBadgeRect(edge)
	circleSafe := defaultAvatarBadgeOptions()
	circleSafe.layout = "circle-safe"
	safeX, safeY, _ := avatarBadgeRect(circleSafe)
	if edgeX <= safeX || edgeY <= safeY {
		t.Fatalf("expected edge layout to sit closer to bottom-right than circle-safe, edge=(%d,%d) safe=(%d,%d)", edgeX, edgeY, safeX, safeY)
	}
}

func TestGeneratedGroupAvatarUsesParticipantBubblesAndBadge(t *testing.T) {
	opts := defaultAvatarBadgeOptions()
	opts.shadow = false
	opts.groupAvatarMaxParticipants = 4
	opts.groupAvatarSelfIDs = []string{"self-1"}
	chat := Chat{
		ID:        "!group:beeper",
		AccountID: "whatsapp",
		Network:   "WhatsApp",
		Name:      "Projektgruppe",
		IsGroup:   true,
		Participants: []Sender{
			{ID: "self-1", DisplayName: "Me"},
			{DisplayName: "Anna Novak"},
			{DisplayName: "Ben Berger"},
			{DisplayName: "Clara Chen"},
			{DisplayName: "David Duran"},
			{DisplayName: "Eva Eder"},
		},
	}

	media := generatedContactAvatarMediaWithOptions(chat, opts)
	if !strings.HasPrefix(media.AssetID, "avatar-fallback-v17:!group:beeper:group-4-") || !strings.Contains(media.AssetID, "excludeself") {
		t.Fatalf("expected group-aware v17 fallback asset id, got %q", media.AssetID)
	}
	labels := groupAvatarParticipantLabels(chat, opts)
	if len(labels) != 4 || labels[0] != "Anna Novak" || labels[3] != "+2" {
		t.Fatalf("expected self-excluded group labels with overflow, got %#v", labels)
	}
	body, err := io.ReadAll(media.Content)
	if err != nil {
		t.Fatalf("read group avatar: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode group avatar: %v", err)
	}
	bubbles := groupAvatarBubbleLayout(labels, opts.groupAvatarOverlapPercent)
	samples := make([]color.RGBA, 0, len(bubbles))
	for _, bubble := range bubbles {
		samples = append(samples, rgbaAt(img, bubble.cx, bubble.cy))
	}
	if samples[0] == samples[1] || samples[0] == samples[2] || samples[1] == samples[3] {
		t.Fatalf("expected participant bubbles to use distinct deterministic colors, got %#v", samples)
	}
	if rgbaAt(img, 221, 221) == samples[3] {
		t.Fatal("expected platform badge to affect the lower-right corner")
	}
}

func TestSingleVisibleGroupParticipantUsesFullInitialsFallback(t *testing.T) {
	opts := defaultAvatarBadgeOptions()
	opts.groupAvatarSelfIDs = []string{"self-1"}
	chat := Chat{
		ID:        "!single-visible:beeper",
		AccountID: "telegram",
		Network:   "Telegram",
		Name:      "Projektgruppe",
		IsGroup:   true,
		Participants: []Sender{
			{ID: "self-1", DisplayName: "Ich", MessageCount: 50},
			{DisplayName: "Hannah Huber", MessageCount: 12},
		},
	}

	participant, ok := singleParticipantGroupFallback(chat, opts)
	if !ok {
		t.Fatal("expected single visible group participant fallback")
	}
	if participant.label != "Hannah Huber" {
		t.Fatalf("expected visible participant label, got %q", participant.label)
	}
	if shouldDrawGroupFallback(chat, opts) {
		t.Fatal("did not expect one visible participant to render as a tiny group bubble")
	}
	media := generatedContactAvatarMediaWithOptions(chat, opts)
	body, err := io.ReadAll(media.Content)
	if err != nil {
		t.Fatalf("read single participant avatar: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode single participant avatar: %v", err)
	}
	if luminance(rgbaAt(img, 128, 132)) < 150 {
		t.Fatal("expected large white participant initials near the center")
	}
}

func TestGroupAvatarWeightsBubblesByMessageCount(t *testing.T) {
	opts := defaultAvatarBadgeOptions()
	opts.groupAvatarMaxParticipants = 4
	opts.groupAvatarSelfIDs = []string{"self-1"}
	chat := Chat{
		ID:        "!weighted:beeper",
		AccountID: "signal",
		Network:   "Signal",
		Name:      "Weighted Group",
		IsGroup:   true,
		Participants: []Sender{
			{ID: "self-1", DisplayName: "Ich", MessageCount: 999},
			{DisplayName: "Low Volume", MessageCount: 8},
			{DisplayName: "Top Writer", MessageCount: 240},
			{DisplayName: "Middle Writer", MessageCount: 90},
			{DisplayName: "Quiet Reader", MessageCount: 2},
			{DisplayName: "Silent Member", MessageCount: 1},
		},
	}

	participants := groupAvatarParticipants(chat, opts)
	if got := []string{participants[0].label, participants[1].label, participants[2].label, participants[3].label}; got[0] != "Top Writer" || got[1] != "Middle Writer" || got[2] != "Low Volume" || got[3] != "+2" {
		t.Fatalf("expected participants to sort by message count with hidden overflow, got %#v", got)
	}
	bubbles := groupAvatarBubbleLayoutForParticipants(participants, opts.groupAvatarOverlapPercent)
	if len(bubbles) != 4 {
		t.Fatalf("expected 4 bubbles, got %d", len(bubbles))
	}
	if !(bubbles[0].r > bubbles[1].r && bubbles[1].r > bubbles[2].r && bubbles[3].r <= bubbles[2].r) {
		t.Fatalf("expected bubble radii to follow message volume, got %#v", bubbles)
	}

	media := generatedContactAvatarMediaWithOptions(chat, opts)
	body, err := io.ReadAll(media.Content)
	if err != nil {
		t.Fatalf("read weighted group avatar: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(body))
	if err != nil {
		t.Fatalf("decode weighted group avatar: %v", err)
	}
	if rgbaAt(img, bubbles[0].cx, bubbles[0].cy) == rgbaAt(img, bubbles[2].cx, bubbles[2].cy) {
		t.Fatal("expected weighted bubbles to render with distinct visible colors")
	}
}

func TestGroupAvatarBubblesHaveVisiblePadding(t *testing.T) {
	participants := []groupAvatarParticipant{
		{label: "Anna Novak", messageCount: 240},
		{label: "Ben Berger", messageCount: 128},
		{label: "Clara Chen", messageCount: 54},
		{label: "David Duran", messageCount: 17},
		{label: "Eva Eder", messageCount: 6},
		{label: "Felix Faber", messageCount: 2},
	}
	bubbles := groupAvatarBubbleLayoutForParticipants(participants, 34)
	if len(bubbles) != 6 {
		t.Fatalf("expected 6 bubbles, got %d", len(bubbles))
	}
	for i := 0; i < len(bubbles); i++ {
		for j := i + 1; j < len(bubbles); j++ {
			dx := bubbles[i].cx - bubbles[j].cx
			dy := bubbles[i].cy - bubbles[j].cy
			gap := math.Sqrt(float64(dx*dx+dy*dy)) - float64(bubbles[i].r+bubbles[j].r)
			if gap < float64(groupAvatarBubbleGap(34)-1) {
				t.Fatalf("expected uniform visible padding between %q and %q, got gap %.1fpx; bubbles=%#v", bubbles[i].label, bubbles[j].label, gap, bubbles)
			}
		}
	}
	badgeX, badgeY, badgeSize := avatarBadgeRect(defaultAvatarBadgeOptions())
	badgeCX := badgeX + badgeSize/2
	badgeCY := badgeY + badgeSize/2
	badgeR := float64(badgeSize) * 0.707
	for _, bubble := range bubbles {
		dx := bubble.cx - badgeCX
		dy := bubble.cy - badgeCY
		gap := math.Sqrt(float64(dx*dx+dy*dy)) - float64(bubble.r) - badgeR
		if gap < float64(groupAvatarBadgeGap(groupAvatarBubbleGap(34))-1) {
			t.Fatalf("expected uniform visible padding between bubble %q and platform badge, got gap %.1fpx; bubbles=%#v", bubble.label, gap, bubbles)
		}
	}
}

func TestBeeperPastelAvatarStyleUsesWhiteText(t *testing.T) {
	cases := map[string]struct {
		background string
		text       string
	}{
		"A": {background: "#d69e51", text: "#ffffff"},
		"B": {background: "#51b9d6", text: "#ffffff"},
		"C": {background: "#51d684", text: "#ffffff"},
		"D": {background: "#d6518f", text: "#ffffff"},
	}
	for name, want := range cases {
		style := uiAvatarStyle(name)
		if got := hexColor(style.background); got != want.background {
			t.Fatalf("expected Beeper pastel background for %q to be %s, got %s", name, want.background, got)
		}
		if got := hexColor(style.textColor); got != want.text {
			t.Fatalf("expected white Beeper initials for %q to be %s, got %s", name, want.text, got)
		}
	}
}

func TestUIAvatarInitialsMatchVendoredGenerator(t *testing.T) {
	cases := map[string]string{
		"John Doe":           "JD",
		"John":               "JO",
		"MA":                 "MA",
		"John Doe Bergerson": "JB",
		"Gustav Årgonson":    "GÅ",
		"Chanel Butterman":   "CB",
	}
	for name, want := range cases {
		if got := uiAvatarInitials(name); got != want {
			t.Fatalf("expected UI Avatars initials for %q to be %q, got %q", name, want, got)
		}
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

func TestVisibleBoundsIgnoresTransparentIconPadding(t *testing.T) {
	icon := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(icon, image.Rect(36, 40, 92, 96), &image.Uniform{C: color.RGBA{R: 230, G: 40, B: 40, A: 255}}, image.Point{}, draw.Src)

	got := visibleBounds(icon)
	want := image.Rect(36, 40, 92, 96)
	if got != want {
		t.Fatalf("expected visible icon bounds %v, got %v", want, got)
	}
}

func TestDrawNormalizedIconKeepsServiceIconsSameSize(t *testing.T) {
	tight := image.NewRGBA(image.Rect(0, 0, 48, 48))
	draw.Draw(tight, tight.Bounds(), &image.Uniform{C: color.RGBA{R: 40, G: 120, B: 230, A: 255}}, image.Point{}, draw.Src)

	padded := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(padded, image.Rect(38, 34, 90, 86), &image.Uniform{C: color.RGBA{R: 40, G: 120, B: 230, A: 255}}, image.Point{}, draw.Src)

	tightCanvas := image.NewRGBA(image.Rect(0, 0, 96, 96))
	paddedCanvas := image.NewRGBA(image.Rect(0, 0, 96, 96))
	drawNormalizedIcon(tightCanvas, tight, 14, 14, 68, 68, 17)
	drawNormalizedIcon(paddedCanvas, padded, 14, 14, 68, 68, 17)

	tightBounds := visibleBounds(tightCanvas)
	paddedBounds := visibleBounds(paddedCanvas)
	if tightBounds.Dx() != paddedBounds.Dx() || tightBounds.Dy() != paddedBounds.Dy() {
		t.Fatalf("expected normalized icon sizes to match, tight=%v padded=%v", tightBounds, paddedBounds)
	}
	if tightBounds.Dx() < 64 || tightBounds.Dy() < 64 {
		t.Fatalf("expected icons to fill the badge consistently, got %v", tightBounds)
	}
}

func BenchmarkAddPlatformBadgeToAvatar(b *testing.B) {
	img := image.NewRGBA(image.Rect(0, 0, 128, 128))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: color.RGBA{R: 40, G: 90, B: 170, A: 255}}, image.Point{}, draw.Src)
	var originalBuf bytes.Buffer
	if err := png.Encode(&originalBuf, img); err != nil {
		b.Fatal(err)
	}
	original := originalBuf.Bytes()
	chat := Chat{AccountID: "whatsapp", Network: "WhatsApp", Name: "Doris"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		avatar := &MatrixMedia{
			AssetID:     "avatar-asset",
			ContentHash: "old-hash",
			Content:     bytes.NewReader(original),
			FileName:    "person.png",
			MimeType:    "image/png",
			SizeBytes:   int64(len(original)),
		}
		if _, err := addPlatformBadgeToAvatar(chat, avatar); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGeneratedContactAvatarMedia(b *testing.B) {
	chat := Chat{ID: "!chat:beeper", AccountID: "telegram", Network: "Telegram", Name: "No Photo"}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if generatedContactAvatarMedia(chat) == nil {
			b.Fatal("missing generated avatar")
		}
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

func rgbaAt(img image.Image, x, y int) color.RGBA {
	r16, g16, b16, a16 := img.At(x, y).RGBA()
	return color.RGBA{R: uint8(r16 >> 8), G: uint8(g16 >> 8), B: uint8(b16 >> 8), A: uint8(a16 >> 8)}
}

func luminance(c color.RGBA) int {
	return (299*int(c.R) + 587*int(c.G) + 114*int(c.B)) / 1000
}

func hexColor(c color.RGBA) string {
	return fmt.Sprintf("#%02x%02x%02x", c.R, c.G, c.B)
}
