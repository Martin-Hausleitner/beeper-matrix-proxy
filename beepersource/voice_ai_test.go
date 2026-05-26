package beepersource

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalVoiceAIProcessorRunsCommandAndOpenAICompatibleSummary(t *testing.T) {
	ctx := context.Background()
	markerPath := filepath.Join(t.TempDir(), "runtime-marker.txt")
	t.Setenv("VOICE_AI_TEST_MARKER", markerPath)
	var requestedModel string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected summary path %s", r.URL.Path)
		}
		var body struct {
			Model    string `json:"model"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode summary request: %v", err)
		}
		requestedModel = body.Model
		if len(body.Messages) < 2 || !strings.Contains(body.Messages[1].Content, "roh transkript") {
			t.Fatalf("expected transcript in summary prompt, got %#v", body.Messages)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"Kurzfassung auf Deutsch."}}]}`))
	}))
	defer server.Close()
	audioPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	processor := NewLocalVoiceAIProcessor(VoiceAIConfig{
		Enabled:               true,
		TranscribeCommand:     "printf 'roh transkript'",
		StartCommand:          "printf start > \"$VOICE_AI_TEST_MARKER\"",
		StopCommand:           "printf stop >> \"$VOICE_AI_TEST_MARKER\"",
		SummaryBaseURL:        server.URL,
		SummaryModel:          "matrix-summarizer",
		SummaryTimeoutSeconds: 5,
		Language:              "auto",
	})

	result, err := processor.ProcessVoice(ctx, VoiceAIInput{
		Chat:      Chat{Network: "Signal", Name: "Signal Test"},
		Message:   Message{ID: "$voice"},
		Sender:    Sender{DisplayName: "Felix"},
		AudioPath: audioPath,
	})
	if err != nil {
		t.Fatalf("ProcessVoice returned error: %v", err)
	}
	if result.Transcript != "roh transkript" || result.Summary != "Kurzfassung auf Deutsch." || result.Model != "matrix-summarizer" {
		t.Fatalf("unexpected voice AI result: %#v", result)
	}
	if requestedModel != "matrix-summarizer" {
		t.Fatalf("expected configured summary model, got %q", requestedModel)
	}
	marker, err := os.ReadFile(markerPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(marker) != "startstop" {
		t.Fatalf("expected start and stop hooks to run, got %q", string(marker))
	}
}

func TestLocalVoiceAIProcessorTimesOutHungCommand(t *testing.T) {
	ctx := context.Background()
	audioPath := filepath.Join(t.TempDir(), "voice.ogg")
	if err := os.WriteFile(audioPath, []byte("audio"), 0o600); err != nil {
		t.Fatal(err)
	}
	processor := NewLocalVoiceAIProcessor(VoiceAIConfig{
		Enabled:               true,
		TranscribeCommand:     "sleep 2",
		CommandTimeoutSeconds: 1,
		SummaryTimeoutSeconds: 5,
		Language:              "auto",
	})

	_, err := processor.ProcessVoice(ctx, VoiceAIInput{AudioPath: audioPath})
	if err == nil {
		t.Fatal("expected hung transcription command to time out")
	}
}

func TestCleanupVoiceAISummaryRemovesThinkingBlocks(t *testing.T) {
	got := cleanupVoiceAISummary("<think>\nich analysiere erst\n</think>\n/no_think\nKurz: Martin bringt um 15 Uhr die Unterlagen mit.")
	want := "Kurz: Martin bringt um 15 Uhr die Unterlagen mit."
	if got != want {
		t.Fatalf("cleanup summary = %q, want %q", got, want)
	}
}

func TestVoiceAIAllowlistMatchesExactly(t *testing.T) {
	cfg := DefaultConfig()
	cfg.VoiceAI.Enabled = true
	cfg.VoiceAI.AllowNetworks = []string{"Signal"}
	cfg.VoiceAI.AllowChatNames = []string{"Felix"}
	svc := &Service{cfg: cfg}
	msg := Message{Type: MessageTypeVoice}
	att := Attachment{URL: "beeper://voice", MimeType: "audio/ogg", IsVoiceNote: true}

	if svc.allowsVoiceAI(Chat{Network: "Signal", Name: "Not Felix"}, msg, Sender{}, att) {
		t.Fatal("expected partial chat-name match to be rejected")
	}
	if !svc.allowsVoiceAI(Chat{Network: "Signal", Name: "Felix"}, msg, Sender{}, att) {
		t.Fatal("expected exact chat-name match to be accepted")
	}
}

func TestSanitizedVoiceAIErrorRemovesSensitiveDetails(t *testing.T) {
	got := sanitizedVoiceAIError(os.ErrPermission)
	if got == "" || strings.Contains(got, "Bearer") {
		t.Fatalf("unexpected sanitized ordinary error: %q", got)
	}

	sensitive := sanitizedVoiceAIError(errors.New("Authorization Bearer secret Transcript: private words"))
	if strings.Contains(sensitive, "secret") || strings.Contains(sensitive, "private words") {
		t.Fatalf("sanitized error leaked sensitive detail: %q", sensitive)
	}
	if sensitive == "" {
		t.Fatal("expected sanitized sensitive error message")
	}
}
