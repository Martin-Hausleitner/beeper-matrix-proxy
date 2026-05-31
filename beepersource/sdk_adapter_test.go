package beepersource

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDesktopAPIAdapterHealthUsesInfoEndpoint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/info" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"app":       map[string]any{"bundle_id": "com.beeper", "name": "Beeper", "version": "4.2.830"},
			"endpoints": map[string]any{"mcp": "/v0/mcp", "oauth": map[string]string{}, "spec": "/openapi.json", "ws_events": "/v1/ws"},
			"platform":  map[string]any{"arch": "arm64", "os": "darwin"},
			"server":    map[string]any{"base_url": serverURL(r), "hostname": "localhost", "mcp_enabled": true, "port": 23373, "remote_access": false, "status": "ok"},
		})
	}))
	defer server.Close()

	adapter := NewDesktopAPIAdapter(Config{Beeper: BeeperConfig{BaseURL: server.URL}}, "test-token")

	if err := adapter.Health(context.Background()); err != nil {
		t.Fatalf("Health returned error: %v", err)
	}
}

func TestDesktopAPIAdapterListChatsUsesConfiguredChatIDs(t *testing.T) {
	var gotPaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPaths = append(gotPaths, r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id":        strings.TrimPrefix(r.URL.Path, "/v1/chats/"),
			"accountID": "signal",
			"title":     "Test",
			"type":      "group",
		})
	}))
	defer server.Close()

	adapter := NewDesktopAPIAdapter(Config{Beeper: BeeperConfig{
		BaseURL: server.URL,
		ChatIDs: []string{"!signal:beeper", "!whatsapp:beeper"},
	}}, "test-token")
	chats, err := adapter.ListChats(context.Background())

	if err != nil {
		t.Fatalf("ListChats returned error: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected 2 chats, got %d", len(chats))
	}
	if strings.Join(gotPaths, ",") != "/v1/chats/!signal:beeper,/v1/chats/!whatsapp:beeper" {
		t.Fatalf("unexpected paths %v", gotPaths)
	}
}

func TestDesktopAPIAdapterListChatsAutoPagesAllChats(t *testing.T) {
	var gotCursors []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCursors = append(gotCursors, r.URL.Query().Get("cursor"))
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Query().Get("cursor") == "" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"items": []map[string]any{{
					"id":         "!one:beeper",
					"accountID":  "whatsapp",
					"network":    "WhatsApp",
					"title":      "One",
					"type":       "group",
					"isArchived": true,
					"participants": map[string]any{
						"hasMore": false,
						"total":   1,
						"items": []map[string]any{{
							"id":       "@alice:whatsapp",
							"fullName": "Alice",
							"imgURL":   "file:///tmp/alice.png",
						}},
					},
				}},
				"hasMore":      true,
				"oldestCursor": "page-2",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"items": []map[string]any{{
				"id":        "!two:beeper",
				"accountID": "signal",
				"network":   "Signal",
				"title":     "Two",
				"type":      "group",
			}},
			"hasMore": false,
		})
	}))
	defer server.Close()

	adapter := NewDesktopAPIAdapter(Config{Beeper: BeeperConfig{BaseURL: server.URL}}, "test-token")
	chats, err := adapter.ListChats(context.Background())
	if err != nil {
		t.Fatalf("ListChats returned error: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected two chats from both pages, got %d", len(chats))
	}
	if chats[0].Network != "WhatsApp" || chats[1].Network != "Signal" {
		t.Fatalf("expected network names to be preserved, got %#v", chats)
	}
	if !chats[0].IsArchived {
		t.Fatalf("expected archived flag to be preserved, got %#v", chats[0])
	}
	if len(chats[0].Participants) != 1 || chats[0].Participants[0].AvatarID != "file:///tmp/alice.png" {
		t.Fatalf("expected participant avatar to be preserved, got %#v", chats[0].Participants)
	}
	if strings.Join(gotCursors, ",") != ",page-2" {
		t.Fatalf("expected cursor pagination, got %v", gotCursors)
	}
}

func TestDesktopAPIAdapterSendMessageUsesBearerTokenAndReplyID(t *testing.T) {
	var gotAuth string
	var gotPath string
	var body map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotPath = r.URL.Path
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"chatID":           "!chat:beeper",
			"pendingMessageID": "$pending",
		})
	}))
	defer server.Close()

	adapter := NewDesktopAPIAdapter(Config{Beeper: BeeperConfig{BaseURL: server.URL}}, "test-token")
	pendingID, err := adapter.SendMessage(context.Background(), BeeperOutbound{
		ChatID:    "!chat:beeper",
		Text:      "hello",
		ReplyToID: "$reply",
	})

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if pendingID != "$pending" {
		t.Fatalf("unexpected pending ID %q", pendingID)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("expected bearer token, got %q", gotAuth)
	}
	if gotPath != "/v1/chats/!chat:beeper/messages" {
		t.Fatalf("unexpected path %s", gotPath)
	}
	if body["text"] != "hello" || body["replyToMessageID"] != "$reply" {
		t.Fatalf("unexpected request body %#v", body)
	}
}

func TestDesktopAPIAdapterSendMessageUploadsAttachmentWithMetadata(t *testing.T) {
	var uploadFileName string
	var uploadContentType string
	var sendBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/assets/upload":
			reader, err := r.MultipartReader()
			if err != nil {
				t.Fatalf("multipart reader: %v", err)
			}
			for {
				part, err := reader.NextPart()
				if err == io.EOF {
					break
				}
				if err != nil {
					t.Fatalf("next part: %v", err)
				}
				if part.FormName() == "file" {
					uploadFileName = part.FileName()
					uploadContentType = part.Header.Get("Content-Type")
				}
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{
				"uploadID": "upload-1",
				"fileName": "cinny.png",
				"mimeType": "image/png",
				"width":    1,
				"height":   1,
			})
		case "/v1/chats/!chat:beeper/messages":
			if err := json.NewDecoder(r.Body).Decode(&sendBody); err != nil {
				t.Fatalf("decode send body: %v", err)
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"chatID": "!chat:beeper", "pendingMessageID": "$pending"})
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()

	adapter := NewDesktopAPIAdapter(Config{Beeper: BeeperConfig{BaseURL: server.URL}}, "test-token")
	_, err := adapter.SendMessage(context.Background(), BeeperOutbound{
		ChatID: "!chat:beeper",
		Text:   "caption",
		Attachment: &OutboundAttachment{
			Content:  io.NopCloser(strings.NewReader("png")),
			FileName: "cinny.png",
			MimeType: "image/png",
			Type:     "image",
		},
	})

	if err != nil {
		t.Fatalf("SendMessage returned error: %v", err)
	}
	if uploadFileName != "cinny.png" || uploadContentType != "image/png" {
		t.Fatalf("unexpected upload metadata filename=%q content-type=%q", uploadFileName, uploadContentType)
	}
	attachment, ok := sendBody["attachment"].(map[string]any)
	if !ok || attachment["uploadID"] != "upload-1" || attachment["type"] != "image" {
		t.Fatalf("unexpected send attachment %#v", sendBody["attachment"])
	}
}

func serverURL(r *http.Request) string {
	return "http://" + r.Host
}
