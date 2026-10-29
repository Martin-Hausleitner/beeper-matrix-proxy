package beepersource

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func TestLocalVoiceAIProcessorIntegration(t *testing.T) {
	audioPath := strings.TrimSpace(os.Getenv("BEEPER_MATRIX_PROXY_VOICE_AI_INTEGRATION_AUDIO"))
	if audioPath == "" {
		t.Skip("set BEEPER_MATRIX_PROXY_VOICE_AI_INTEGRATION_AUDIO to run the local Whisper/LLM integration proof")
	}
	cfg := DefaultConfig().VoiceAI
	cfg.Enabled = true
	if cfg.TranscribeCommand == "" {
		t.Fatal("BEEPER_MATRIX_PROXY_VOICE_AI_TRANSCRIBE_COMMAND is required")
	}
	if cfg.SummaryModel == "" {
		t.Fatal("BEEPER_MATRIX_PROXY_VOICE_AI_SUMMARY_MODEL is required")
	}
	processor := NewLocalVoiceAIProcessor(cfg)
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.CommandTimeoutSeconds+cfg.SummaryTimeoutSeconds+30)*time.Second)
	defer cancel()
	result, err := processor.ProcessVoice(ctx, VoiceAIInput{
		Chat:       Chat{Name: "Signal Test", Network: "Signal"},
		Sender:     Sender{DisplayName: "Martin"},
		Attachment: Attachment{FileName: "matrix-voice-proof.wav", MimeType: "audio/wav", IsVoiceNote: true},
		AudioPath:  audioPath,
	})
	if err != nil {
		t.Fatalf("local voice AI integration failed: %v", err)
	}
	if !strings.Contains(strings.ToLower(result.Transcript), "martin") {
		t.Fatalf("transcript did not contain expected proof word: %q", result.Transcript)
	}
	if strings.TrimSpace(result.Summary) == "" {
		t.Fatal("summary was empty")
	}
	t.Logf("transcript_bytes=%d summary_bytes=%d", len(result.Transcript), len(result.Summary))
}

func TestVoiceAILiveReplay(t *testing.T) {
	rawIDs := strings.TrimSpace(os.Getenv("BEEPER_MATRIX_PROXY_VOICE_AI_LIVE_REPLAY_IDS"))
	if rawIDs == "" {
		t.Skip("set BEEPER_MATRIX_PROXY_VOICE_AI_LIVE_REPLAY_IDS to replay existing local Beeper voice messages")
	}
	dbPath := strings.TrimSpace(os.Getenv("BEEPER_SOURCE_DB"))
	if dbPath == "" {
		dbPath = "beeper-source-all-chats.db"
	}
	cfg := DefaultConfig()
	if !cfg.VoiceAI.Enabled {
		t.Fatal("voice AI must be enabled for live replay")
	}
	beeperToken, err := cfg.BeeperToken()
	if err != nil {
		t.Fatal(err)
	}
	matrixToken, err := cfg.MatrixToken()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	store, err := OpenStore(ctx, dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	api := NewDesktopAPIAdapter(cfg, beeperToken)
	matrix, err := NewMatrixClientSink(cfg, store, matrixToken)
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(cfg, store, api, matrix)
	for _, id := range strings.Split(rawIDs, ",") {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		msg, chat, sender, roomID, eventID, err := loadVoiceAIReplayMessage(ctx, store, id)
		if err != nil {
			t.Fatalf("load replay message %s: %v", id, err)
		}
		version := MessageVersion(msg)
		if suffix := strings.TrimSpace(os.Getenv("BEEPER_MATRIX_PROXY_VOICE_AI_LIVE_REPLAY_SUFFIX")); suffix != "" {
			version += ":" + suffix
		}
		if err := store.SetValue(ctx, voiceAIKVKey(msg.ID, version), ""); err != nil {
			t.Fatalf("clear replay key %s: %v", id, err)
		}
		if err := svc.maybeProcessVoiceAI(ctx, roomID, chat, msg, sender, msg.Attachments[0], msg.ID, eventID, version); err != nil {
			t.Fatalf("live replay %s failed: %v", id, err)
		}
		value, err := store.GetValue(ctx, voiceAIKVKey(msg.ID, version))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(value, "$") {
			t.Fatalf("live replay %s did not store Matrix event ID, got %q", id, value)
		}
		t.Logf("live replay %s posted %s", id, value)
		time.Sleep(2 * time.Second)
	}
}

func loadVoiceAIReplayMessage(ctx context.Context, store *Store, messageID string) (Message, Chat, Sender, string, string, error) {
	var raw string
	err := store.db.QueryRowContext(ctx, "SELECT raw_json FROM beeper_message_raw WHERE beeper_message_id=?", messageID).Scan(&raw)
	if err != nil {
		return Message{}, Chat{}, Sender{}, "", "", err
	}
	var decoded struct {
		ID          string `json:"id"`
		ChatID      string `json:"chatID"`
		AccountID   string `json:"accountID"`
		SenderID    string `json:"senderID"`
		SenderName  string `json:"senderName"`
		Timestamp   string `json:"timestamp"`
		SortKey     string `json:"sortKey"`
		Type        string `json:"type"`
		Text        string `json:"text"`
		IsSender    bool   `json:"isSender"`
		IsDeleted   bool   `json:"isDeleted"`
		Attachments []struct {
			ID       string `json:"id"`
			Type     string `json:"type"`
			MimeType string `json:"mimeType"`
			FileName string `json:"fileName"`
			FileSize int64  `json:"fileSize"`
			SrcURL   string `json:"srcURL"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return Message{}, Chat{}, Sender{}, "", "", err
	}
	if len(decoded.Attachments) == 0 {
		return Message{}, Chat{}, Sender{}, "", "", sql.ErrNoRows
	}
	timestamp, _ := time.Parse(time.RFC3339Nano, decoded.Timestamp)
	att := decoded.Attachments[0]
	msg := Message{
		ID:         decoded.ID,
		AccountID:  decoded.AccountID,
		ChatID:     decoded.ChatID,
		SenderID:   decoded.SenderID,
		SenderName: decoded.SenderName,
		SortKey:    decoded.SortKey,
		Type:       decoded.Type,
		Text:       decoded.Text,
		Timestamp:  timestamp,
		IsSender:   decoded.IsSender,
		IsDeleted:  decoded.IsDeleted,
		RawJSON:    raw,
		Attachments: []Attachment{{
			ID:          att.ID,
			URL:         firstNonEmpty(att.SrcURL, att.ID),
			FileName:    att.FileName,
			MimeType:    att.MimeType,
			SizeBytes:   att.FileSize,
			IsVoiceNote: strings.EqualFold(att.Type, "audio"),
		}},
	}
	var roomID, matrixEventID string
	if err := store.db.QueryRowContext(ctx, "SELECT matrix_event_id FROM message_mapping WHERE beeper_message_id=?", messageID).Scan(&matrixEventID); err != nil {
		return Message{}, Chat{}, Sender{}, "", "", err
	}
	chat, ok, err := store.PortalChat(ctx, decoded.ChatID)
	if err != nil {
		return Message{}, Chat{}, Sender{}, "", "", err
	}
	if !ok {
		chat = Chat{ID: decoded.ChatID, AccountID: decoded.AccountID}
	}
	if storedRoomID, ok, err := store.PortalRoomID(ctx, decoded.ChatID); err != nil {
		return Message{}, Chat{}, Sender{}, "", "", err
	} else if ok {
		roomID = storedRoomID
	}
	sender := Sender{ID: decoded.SenderID, DisplayName: firstNonEmpty(decoded.SenderName, decoded.SenderID)}
	return msg, chat, sender, roomID, matrixEventID, nil
}
