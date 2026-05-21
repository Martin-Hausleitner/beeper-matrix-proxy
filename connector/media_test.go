package connector

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"maunium.net/go/mautrix/bridgev2/networkid"
	"maunium.net/go/mautrix/crypto/attachment"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

func TestDirectMediaDownloadURLsEscapeMXCParts(t *testing.T) {
	uri := id.ContentURI{
		Homeserver: "matrix.example:8448",
		FileID:     "abc/def+ghi",
	}

	urls := directMediaDownloadURLs(uri)

	expected := []string{
		"https://matrix.example:8448/_matrix/media/v3/download/matrix.example:8448/abc%2Fdef+ghi",
		"https://matrix.example:8448/_matrix/media/r0/download/matrix.example:8448/abc%2Fdef+ghi",
	}
	if len(urls) != len(expected) {
		t.Fatalf("expected %d urls, got %d: %#v", len(expected), len(urls), urls)
	}
	for i, want := range expected {
		if urls[i] != want {
			t.Fatalf("url %d mismatch:\nwant %s\n got %s", i, want, urls[i])
		}
	}
}

func TestDownloadMXCDirectDoesNotSendAccessTokenToMXCOrigin(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/_matrix/media/v3/download/") {
			t.Fatalf("expected unauthenticated media endpoint first, got %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "" {
			t.Fatalf("expected no bearer token to be sent to MXC origin, got %q", got)
		}
		_, _ = w.Write([]byte("media"))
	}))
	defer server.Close()

	oldClient := matrixMediaHTTPClient
	matrixMediaHTTPClient = server.Client()
	t.Cleanup(func() {
		matrixMediaHTTPClient = oldClient
	})

	data, err := downloadMXCDirect(t.Context(), id.ContentURI{
		Homeserver: strings.TrimPrefix(server.URL, "https://"),
		FileID:     "abc",
	}, defaultDirectMediaMaxBytes)
	if err != nil {
		t.Fatalf("downloadMXCDirect returned error: %v", err)
	}
	if string(data) != "media" {
		t.Fatalf("expected media bytes, got %q", data)
	}
}

func TestDirectMXCFallbackRequiresAllowlist(t *testing.T) {
	uri := id.ContentURI{Homeserver: "matrix.example", FileID: "abc"}
	t.Setenv("LOCAL_MATRIX_DIRECT_MXC_FALLBACK_ALLOWLIST", "")
	if directMXCFallbackAllowed(uri) {
		t.Fatal("expected direct MXC fallback to be disabled by default")
	}
	t.Setenv("LOCAL_MATRIX_DIRECT_MXC_FALLBACK_ALLOWLIST", "other.example, matrix.example ")
	if !directMXCFallbackAllowed(uri) {
		t.Fatal("expected direct MXC fallback to allow explicit homeserver")
	}
}

func TestReadMatrixMediaResponseRejectsOversizedBody(t *testing.T) {
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader("abcdef")),
	}

	_, err := readMatrixMediaResponse(resp, 5)
	if err == nil {
		t.Fatal("expected oversized body to be rejected")
	}
}

func TestReadMatrixMediaResponseRejectsOversizedContentLength(t *testing.T) {
	resp := &http.Response{
		StatusCode:    http.StatusOK,
		ContentLength: 6,
		Body:          io.NopCloser(strings.NewReader("abcdef")),
	}

	_, err := readMatrixMediaResponse(resp, 5)
	if err == nil {
		t.Fatal("expected oversized content length to be rejected before full read")
	}
}

func TestDirectMediaMaxBytesUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("LOCAL_MATRIX_DIRECT_MEDIA_MAX_SIZE", "42")

	if got := directMediaMaxBytes(); got != 42 {
		t.Fatalf("expected env override 42, got %d", got)
	}
}

func TestDirectMediaIDRoundTrip(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_DIRECT_MEDIA_KEY", "test-key")
	mediaID, err := encodeDirectMediaID("login-1", id.ContentURIString("mxc://matrix.example/media"))
	if err != nil {
		t.Fatalf("encodeDirectMediaID returned error: %v", err)
	}

	payload, err := decodeDirectMediaID(mediaID)
	if err != nil {
		t.Fatalf("decodeDirectMediaID returned error: %v", err)
	}
	if payload.Version != 2 || payload.LoginID != "login-1" || payload.MXC != "mxc://matrix.example/media" || payload.Signature == "" {
		t.Fatalf("unexpected direct media payload: %#v", payload)
	}
}

func TestDirectMediaIDRequiresSigningKey(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_DIRECT_MEDIA_KEY", "")
	t.Setenv("BEEPER_MATRIX_PROXY_MEDIA_KEY", "")

	if _, err := encodeDirectMediaID("login-1", id.ContentURIString("mxc://matrix.example/media")); err == nil {
		t.Fatal("expected direct media ID encoding to require a signing key")
	}
}

func TestDirectMediaIDRejectsTampering(t *testing.T) {
	t.Setenv("BEEPER_MATRIX_PROXY_DIRECT_MEDIA_KEY", "test-key")
	mediaID, err := encodeDirectMediaID("login-1", id.ContentURIString("mxc://matrix.example/media"))
	if err != nil {
		t.Fatalf("encodeDirectMediaID returned error: %v", err)
	}
	raw, err := base64.RawURLEncoding.DecodeString(string(mediaID))
	if err != nil {
		t.Fatal(err)
	}
	var payload directMediaPayload
	if err = json.Unmarshal(raw, &payload); err != nil {
		t.Fatal(err)
	}
	payload.MXC = "mxc://matrix.example/other"
	raw, err = json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}

	if _, err = decodeDirectMediaID(networkid.MediaID(base64.RawURLEncoding.EncodeToString(raw))); err == nil {
		t.Fatal("expected tampered direct media ID to be rejected")
	}
}

func TestGenerateFallbackAvatarPNG(t *testing.T) {
	data, err := generateFallbackAvatarPNG("mxc://example.invalid/missing")
	if err != nil {
		t.Fatalf("generateFallbackAvatarPNG returned error: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("fallback avatar is not a valid PNG: %v", err)
	}
	if got := img.Bounds().Dx(); got != fallbackAvatarSize {
		t.Fatalf("expected width %d, got %d", fallbackAvatarSize, got)
	}
	if got := img.Bounds().Dy(); got != fallbackAvatarSize {
		t.Fatalf("expected height %d, got %d", fallbackAvatarSize, got)
	}
}

func TestGeneratedFallbackAvatarUsesCacheForRepeatedMXC(t *testing.T) {
	nc := &MyNetworkClient{}
	uri := id.ContentURIString("mxc://example.invalid/missing")
	first := nc.generatedFallbackAvatarFromMXC(uri)
	if first == nil {
		t.Fatal("expected fallback avatar")
	}
	second := nc.generatedFallbackAvatarFromMXC(uri)
	if second == nil {
		t.Fatal("expected cached fallback avatar")
	}
	if first != second {
		t.Fatal("expected repeated fallback avatar calls to reuse the cached avatar object")
	}

	allocs := testing.AllocsPerRun(1000, func() {
		avatar := nc.generatedFallbackAvatarFromMXC(uri)
		if avatar == nil {
			t.Fatal("expected cached fallback avatar")
		}
	})

	if allocs > 1 {
		t.Fatalf("expected repeated fallback avatar generation to be cached, got %.1f allocs/run", allocs)
	}
}

func TestNormalizeMediaContentForBeeperMarksAnimatedGIF(t *testing.T) {
	content := &event.MessageEventContent{
		MsgType: event.MsgVideo,
		Body:    "animation.mp4",
		Info: &event.FileInfo{
			MimeType: "video/mp4",
			MauGIF:   true,
		},
	}

	normalizeMediaContentForBeeper(content)

	if content.Info == nil {
		t.Fatal("expected file info")
	}
	if !content.Info.MauGIF {
		t.Fatal("expected fi.mau.gif to remain set")
	}
	for _, key := range []string{"fi.mau.loop", "fi.mau.autoplay", "fi.mau.hide_controls", "fi.mau.no_audio"} {
		if got, ok := content.Info.Extra[key].(bool); !ok || !got {
			t.Fatalf("expected %s=true in info extra, got %#v", key, content.Info.Extra[key])
		}
	}
}

func TestNormalizeMediaContentForBeeperAddsVoiceFallback(t *testing.T) {
	content := &event.MessageEventContent{
		MsgType:      event.MsgAudio,
		Body:         "voice.ogg",
		Info:         &event.FileInfo{MimeType: "audio/ogg", Duration: 1234},
		MSC3245Voice: &event.MSC3245Voice{},
		MSC1767Audio: nil,
	}

	normalizeMediaContentForBeeper(content)

	if content.MSC1767Audio == nil {
		t.Fatal("expected MSC1767 audio fallback")
	}
	if content.MSC1767Audio.Duration != 1234 {
		t.Fatalf("expected duration from file info, got %d", content.MSC1767Audio.Duration)
	}
	if len(content.MSC1767Audio.Waveform) != fallbackWaveformSamples {
		t.Fatalf("expected %d waveform samples, got %d", fallbackWaveformSamples, len(content.MSC1767Audio.Waveform))
	}
}

func TestNormalizeMediaContentForBeeperRecursesIntoEditsAndGallery(t *testing.T) {
	content := &event.MessageEventContent{
		NewContent: &event.MessageEventContent{
			MsgType: event.MsgImage,
			Body:    "edited.gif",
			Info:    &event.FileInfo{MimeType: "image/gif"},
		},
		BeeperGalleryImages: []*event.MessageEventContent{{
			MsgType:      event.MsgAudio,
			Body:         "gallery-voice.ogg",
			Info:         &event.FileInfo{MimeType: "audio/ogg", Duration: 987},
			MSC3245Voice: &event.MSC3245Voice{},
		}},
	}

	normalizeMediaContentForBeeper(content)

	if content.NewContent == nil || content.NewContent.Info == nil || !content.NewContent.Info.MauGIF {
		t.Fatalf("expected nested edit GIF metadata to be normalized, got %#v", content.NewContent)
	}
	if got := content.NewContent.Info.Extra["fi.mau.autoplay"]; got != true {
		t.Fatalf("expected nested GIF autoplay flag, got %#v", content.NewContent.Info.Extra)
	}
	gallery := content.BeeperGalleryImages[0]
	if gallery.MSC1767Audio == nil {
		t.Fatal("expected gallery voice fallback")
	}
	if gallery.MSC1767Audio.Duration != 987 {
		t.Fatalf("expected gallery voice duration 987, got %d", gallery.MSC1767Audio.Duration)
	}
	if len(gallery.MSC1767Audio.Waveform) != fallbackWaveformSamples {
		t.Fatalf("expected gallery voice waveform fallback, got %d samples", len(gallery.MSC1767Audio.Waveform))
	}
}

func TestNormalizeVoiceInfoKeepsExistingWaveformAndDuration(t *testing.T) {
	content := &event.MessageEventContent{
		MsgType:      event.MsgAudio,
		Info:         &event.FileInfo{MimeType: "audio/ogg", Duration: 9999},
		MSC3245Voice: &event.MSC3245Voice{},
		MSC1767Audio: &event.MSC1767Audio{
			Duration: 321,
			Waveform: []int{1, 2, 3},
		},
	}

	normalizeMediaContentForBeeper(content)

	if content.MSC1767Audio.Duration != 321 {
		t.Fatalf("expected existing duration to be preserved, got %d", content.MSC1767Audio.Duration)
	}
	if got := content.MSC1767Audio.Waveform; len(got) != 3 || got[0] != 1 || got[2] != 3 {
		t.Fatalf("expected existing waveform to be preserved, got %#v", got)
	}
}

func TestGetLocalMaxUploadSizeUsesEnvironmentOverride(t *testing.T) {
	t.Setenv("LOCAL_MATRIX_MAX_UPLOAD_SIZE", "12345")
	nc := &MyNetworkClient{localMaxUploadSize: 98765}

	if got := nc.getLocalMaxUploadSize(); got != 12345 {
		t.Fatalf("expected env override 12345, got %d", got)
	}
}

func TestGetLocalMaxUploadSizeDoesNotAdvertiseAboveSynapseLimit(t *testing.T) {
	t.Setenv("LOCAL_MATRIX_MAX_UPLOAD_SIZE", "52428800")
	nc := &MyNetworkClient{localMaxUploadSize: 1048576}

	if got := nc.getLocalMaxUploadSize(); got != 1048576 {
		t.Fatalf("expected fetched Synapse limit 1048576 to cap env override, got %d", got)
	}
}

func TestCloneMessageContentKeepsHotPathAllocationsLow(t *testing.T) {
	content := &event.MessageEventContent{
		MsgType:   event.MsgText,
		Body:      "hello",
		RelatesTo: (&event.RelatesTo{}).SetReplyTo("$reply:example"),
		Mentions:  &event.Mentions{UserIDs: []id.UserID{"@alice:example"}},
	}

	allocs := testing.AllocsPerRun(1000, func() {
		clone := cloneMessageContent(content)
		clone.Body = "changed"
		clone.RelatesTo.InReplyTo.EventID = "$other:example"
		clone.Mentions.UserIDs[0] = "@bob:example"
	})

	if content.Body != "hello" || content.RelatesTo.InReplyTo.EventID != "$reply:example" || content.Mentions.UserIDs[0] != "@alice:example" {
		t.Fatalf("clone mutated original content: %#v", content)
	}
	if allocs > 8 {
		t.Fatalf("cloneMessageContent is too allocation-heavy for the sync hot path: got %.1f allocs/run", allocs)
	}
}

func BenchmarkCloneMessageContent(b *testing.B) {
	content := &event.MessageEventContent{
		MsgType:   event.MsgText,
		Body:      strings.Repeat("hello ", 20),
		RelatesTo: (&event.RelatesTo{}).SetReplyTo("$reply:example"),
		Mentions:  &event.Mentions{UserIDs: []id.UserID{"@alice:example", "@bob:example"}},
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = cloneMessageContent(content)
	}
}

func BenchmarkGeneratedFallbackAvatarFromMXC(b *testing.B) {
	nc := &MyNetworkClient{}
	uri := id.ContentURIString("mxc://example.invalid/missing")
	if avatar := nc.generatedFallbackAvatarFromMXC(uri); avatar == nil {
		b.Fatal("expected fallback avatar")
	}
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_ = nc.generatedFallbackAvatarFromMXC(uri)
	}
}

func TestDecryptEncryptedMediaInPlace(t *testing.T) {
	plaintext := []byte("hello encrypted matrix media")
	encrypted := append([]byte(nil), plaintext...)
	file := &event.EncryptedFileInfo{
		EncryptedFile: *attachment.NewEncryptedFile(),
		URL:           id.ContentURIString("mxc://example.invalid/media"),
	}
	file.EncryptInPlace(encrypted)
	if bytes.Equal(encrypted, plaintext) {
		t.Fatal("test setup failed: ciphertext equals plaintext")
	}
	raw, err := json.Marshal(file)
	if err != nil {
		t.Fatalf("failed to marshal encrypted file info: %v", err)
	}
	var decodedFile event.EncryptedFileInfo
	if err = json.Unmarshal(raw, &decodedFile); err != nil {
		t.Fatalf("failed to unmarshal encrypted file info: %v", err)
	}

	err = decryptEncryptedMediaInPlace(encrypted, &decodedFile)
	if err != nil {
		t.Fatalf("decryptEncryptedMediaInPlace returned error: %v", err)
	}
	if !bytes.Equal(encrypted, plaintext) {
		t.Fatalf("expected decrypted plaintext %q, got %q", plaintext, encrypted)
	}
}
