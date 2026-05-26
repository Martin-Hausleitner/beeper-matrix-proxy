package beepersource

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

func TestMatrixClientSinkCreatesRoomAndSendsMessage(t *testing.T) {
	var createdRoom bool
	var createdAvatarURL string
	var createdAvatarMime string
	var sentBody string
	var sentURL string
	var sentFileName string
	var sentReplyTo string
	var sentVoiceAIKind string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/createRoom"):
			createdRoom = true
			var body struct {
				InitialState []struct {
					Type    string `json:"type"`
					Content struct {
						URL  string `json:"url"`
						Info struct {
							MimeType string `json:"mimetype"`
						} `json:"info"`
					} `json:"content"`
				} `json:"initial_state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create room body: %v", err)
			}
			for _, state := range body.InitialState {
				if state.Type == "m.room.avatar" {
					createdAvatarURL = state.Content.URL
					createdAvatarMime = state.Content.Info.MimeType
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"room_id": "!beeper_test:local"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			if ct := r.Header.Get("Content-Type"); ct != "image/png" {
				t.Fatalf("unexpected upload content-type %q", ct)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/uploaded"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/"):
			var body struct {
				Body      string `json:"body"`
				URL       string `json:"url"`
				FileName  string `json:"filename"`
				RelatesTo struct {
					InReplyTo struct {
						EventID string `json:"event_id"`
					} `json:"m.in_reply_to"`
				} `json:"m.relates_to"`
				VoiceAI struct {
					Kind string `json:"kind"`
				} `json:"com.openclaw.voice_ai"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode send body: %v", err)
			}
			sentBody = body.Body
			sentURL = body.URL
			sentFileName = body.FileName
			sentReplyTo = body.RelatesTo.InReplyTo.EventID
			sentVoiceAIKind = body.VoiceAI.Kind
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$event:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	roomID, err := sink.EnsurePortal(ctx, Chat{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Family", IsGroup: true}, &MatrixMedia{
		Content:   bytes.NewReader([]byte("avatar")),
		FileName:  "avatar.png",
		MimeType:  "image/png",
		SizeBytes: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if roomID != "!beeper_test:local" || !createdRoom {
		t.Fatalf("unexpected room result roomID=%q created=%v", roomID, createdRoom)
	}
	if createdAvatarURL != "mxc://local/uploaded" || createdAvatarMime != "image/png" {
		t.Fatalf("expected room avatar to be uploaded into initial state, got url=%q mime=%q", createdAvatarURL, createdAvatarMime)
	}
	eventID, err := sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        roomID,
		MessageID:     "$m1",
		SenderID:      "@alice:whatsapp",
		SenderName:    "Alice",
		Body:          "hello",
		MsgType:       "m.text",
		Timestamp:     time.Unix(100, 0).UTC(),
		TransactionID: "txn1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "$event:local" {
		t.Fatalf("unexpected event ID %q", eventID)
	}
	if sentBody != "Alice: hello" {
		t.Fatalf("unexpected Matrix body %q", sentBody)
	}

	eventID, err = sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        roomID,
		MessageID:     "$m-reply",
		SenderID:      "@alice:whatsapp",
		SenderName:    "Alice",
		Body:          "reply",
		MsgType:       "m.text",
		ReplyToEvent:  "$parent:local",
		TransactionID: "txn-reply",
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "$event:local" {
		t.Fatalf("unexpected reply event ID %q", eventID)
	}
	if sentReplyTo != "$parent:local" {
		t.Fatalf("expected m.in_reply_to target, got %q", sentReplyTo)
	}

	eventID, err = sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        roomID,
		MessageID:     "$voice-ai",
		Body:          "summary",
		MsgType:       "m.notice",
		ReplyToEvent:  "$parent:local",
		TransactionID: "txn-voice-ai",
		VoiceAI: &VoiceAIMetadata{
			SourceMessageID: "$m-reply",
			SourceEventID:   "$parent:local",
			ChatID:          "!chat:beeper",
			Kind:            voiceAIKindTranscriptSummary,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "$event:local" {
		t.Fatalf("unexpected voice AI event ID %q", eventID)
	}
	if sentVoiceAIKind != voiceAIKindTranscriptSummary {
		t.Fatalf("expected voice AI marker in Matrix payload, got %q", sentVoiceAIKind)
	}

	eventID, err = sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        roomID,
		MessageID:     "$m2",
		SenderID:      "@alice:whatsapp",
		SenderName:    "Alice",
		Body:          "image",
		MsgType:       "m.image",
		TransactionID: "txn2",
		Media: &MatrixMedia{
			Content:   bytes.NewReader([]byte("png")),
			FileName:  "image.png",
			MimeType:  "image/png",
			SizeBytes: 3,
			Width:     2,
			Height:    3,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if eventID != "$event:local" {
		t.Fatalf("unexpected media event ID %q", eventID)
	}
	if sentURL != "mxc://local/uploaded" || sentFileName != "image.png" {
		t.Fatalf("unexpected media payload url=%q filename=%q", sentURL, sentFileName)
	}
}

func TestMatrixClientSinkAddsSenderAvatarToPerMessageProfile(t *testing.T) {
	var uploadedContentType string
	var profileAvatarURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadedContentType = r.Header.Get("Content-Type")
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/sender-avatar"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/"):
			var body struct {
				Profile struct {
					AvatarURL string `json:"avatar_url"`
				} `json:"com.beeper.per_message_profile"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode send body: %v", err)
			}
			profileAvatarURL = body.Profile.AvatarURL
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$event:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        "!room:local",
		MessageID:     "$m1",
		SenderID:      "@alice:signal",
		SenderName:    "Alice",
		Body:          "hello",
		MsgType:       "m.text",
		TransactionID: "txn-avatar",
		SenderAvatar: &MatrixMedia{
			AssetID:   "localmxc://alice-avatar",
			Content:   bytes.NewReader([]byte("avatar")),
			FileName:  "alice.png",
			MimeType:  "image/png",
			SizeBytes: 6,
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if uploadedContentType != "image/png" {
		t.Fatalf("expected sender avatar upload content-type, got %q", uploadedContentType)
	}
	if profileAvatarURL != "mxc://local/sender-avatar" {
		t.Fatalf("expected per-message profile avatar URL, got %q", profileAvatarURL)
	}

	_, err = sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        "!room:local",
		MessageID:     "$m2",
		SenderID:      "@alice:signal",
		SenderName:    "Alice",
		Body:          "cached",
		MsgType:       "m.text",
		TransactionID: "txn-avatar-cached",
		SenderAvatar: &MatrixMedia{
			AssetID: "localmxc://alice-avatar",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if profileAvatarURL != "mxc://local/sender-avatar" {
		t.Fatalf("expected cached per-message profile avatar URL, got %q", profileAvatarURL)
	}
}

func TestMatrixClientSinkIgnoresSenderAvatarUploadFailure(t *testing.T) {
	var sent bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			http.Error(w, "too large", http.StatusRequestEntityTooLarge)
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/send/m.room.message/"):
			sent = true
			var body struct {
				Profile struct {
					AvatarURL string `json:"avatar_url"`
				} `json:"com.beeper.per_message_profile"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode send body: %v", err)
			}
			if body.Profile.AvatarURL != "" {
				t.Fatalf("expected failed avatar upload to be omitted, got %q", body.Profile.AvatarURL)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$event:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := sink.SendMessage(ctx, MatrixOutbound{
		RoomID:        "!room:local",
		MessageID:     "$m1",
		SenderID:      "@alice:signal",
		SenderName:    "Alice",
		Body:          "hello",
		MsgType:       "m.text",
		TransactionID: "txn-avatar-fail",
		SenderAvatar: &MatrixMedia{
			AssetID:   "localmxc://alice-avatar",
			Content:   bytes.NewReader([]byte("avatar")),
			FileName:  "alice.png",
			MimeType:  "image/png",
			SizeBytes: 6,
		},
	}); err != nil {
		t.Fatal(err)
	}
	if !sent {
		t.Fatal("expected message send to continue after sender avatar upload failure")
	}
}

func TestPortalProfileCanOmitPlatformFromRoomName(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Matrix.RoomNamePrefix = ""
	cfg.Matrix.RoomNameIncludePlatform = false

	name, topic, value := portalProfileSyncValue(cfg, Chat{
		ID:        "!chat:beeper",
		AccountID: "telegram",
		Network:   "Telegram",
		Name:      "hermes - Macbook",
	})

	if name != "hermes - Macbook" {
		t.Fatalf("expected room name without platform brackets, got %q", name)
	}
	if strings.Contains(name, "[Telegram]") {
		t.Fatalf("expected no bracketed platform in room name, got %q", name)
	}
	if !strings.Contains(topic, "Telegram") {
		t.Fatalf("expected topic to keep service context, got %q", topic)
	}
	if !strings.Contains(value, "hermes - Macbook") {
		t.Fatalf("expected sync value to include profile, got %q", value)
	}
}

func TestPortalProfileOmittingPlatformStripsLegacyImportedPrefix(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Matrix.RoomNamePrefix = ""
	cfg.Matrix.RoomNameIncludePlatform = false

	name, _, _ := portalProfileSyncValue(cfg, Chat{
		ID:        "!chat:beeper",
		AccountID: "telegram",
		Network:   "Telegram",
		Name:      "Beeper: [Telegram] [MM] ALERTS",
	})

	if name != "[MM] ALERTS" {
		t.Fatalf("expected legacy Beeper/platform prefix to be stripped, got %q", name)
	}
}

func TestMatrixClientSinkSpaceParentUsesRequestDeadline(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Sync.PortalTimeoutSeconds = 2
	cli, err := mautrix.NewClient("https://matrix.local", id.UserID("@proxy:local"), "token")
	if err != nil {
		t.Fatal(err)
	}
	cli.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		deadline, ok := req.Context().Deadline()
		if !ok {
			return nil, errors.New("request context has no deadline")
		}
		remaining := time.Until(deadline)
		if remaining <= 0 || remaining > 3*time.Second {
			return nil, errors.New("request context deadline is outside portal timeout")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"event_id":"$state:local"}`)),
			Request:    req,
		}, nil
	})}
	sink := &MatrixClientSink{cfg: cfg, store: store, client: cli}

	if err := sink.linkSpaceParent(ctx, "!child:local", "!parent:local", true); err != nil {
		t.Fatalf("expected space parent link to use deadline-bound request context: %v", err)
	}
}

func TestMatrixClientSinkStateRequestDeadlineIsCapped(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Sync.PortalTimeoutSeconds = 180
	sink := &MatrixClientSink{cfg: cfg}

	reqCtx, cancel := sink.requestContext(context.Background())
	defer cancel()
	deadline, ok := reqCtx.Deadline()
	if !ok {
		t.Fatal("expected request context deadline")
	}
	if remaining := time.Until(deadline); remaining > 31*time.Second {
		t.Fatalf("expected state request timeout to be capped near 30s, got %s", remaining)
	}
}

func TestMatrixClientSinkPortalAccessibleUsesRequestDeadline(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = "https://matrix.local"
	cfg.Sync.PortalTimeoutSeconds = 2
	cli, err := mautrix.NewClient(cfg.Matrix.HomeserverURL, id.UserID("@proxy:local"), "token")
	if err != nil {
		t.Fatal(err)
	}
	cli.Client = &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
		if _, ok := req.Context().Deadline(); !ok {
			return nil, errors.New("request context has no deadline")
		}
		return &http.Response{
			StatusCode: http.StatusOK,
			Header:     make(http.Header),
			Body:       io.NopCloser(strings.NewReader(`{"type":"m.room.create"}`)),
			Request:    req,
		}, nil
	})}
	sink := &MatrixClientSink{cfg: cfg, client: cli, accessToken: "token"}

	ok, err := sink.PortalAccessible(context.Background(), "!room:local")
	if err != nil || !ok {
		t.Fatalf("expected accessible portal with deadline-bound request, ok=%v err=%v", ok, err)
	}
}

func TestMatrixClientSinkReusesCachedPortalAvatar(t *testing.T) {
	uploadCount := 0
	var roomAvatarURLs []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/createRoom"):
			var body struct {
				InitialState []struct {
					Type    string `json:"type"`
					Content struct {
						URL string `json:"url"`
					} `json:"content"`
				} `json:"initial_state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create room body: %v", err)
			}
			for _, state := range body.InitialState {
				if state.Type == "m.room.avatar" {
					roomAvatarURLs = append(roomAvatarURLs, state.Content.URL)
				}
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"room_id": "!room:local"})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/platform-whatsapp"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}
	avatar := func() *MatrixMedia {
		return &MatrixMedia{
			AssetID:   "platform:WhatsApp",
			Content:   bytes.NewReader([]byte("<svg/>")),
			FileName:  "whatsapp.svg",
			MimeType:  "image/svg+xml",
			SizeBytes: 6,
		}
	}

	if _, err = sink.EnsurePortal(ctx, Chat{ID: "!one:beeper", AccountID: "whatsapp", Network: "WhatsApp", Name: "One"}, avatar()); err != nil {
		t.Fatal(err)
	}
	if _, err = sink.EnsurePortal(ctx, Chat{ID: "!two:beeper", AccountID: "whatsapp", Network: "WhatsApp", Name: "Two"}, avatar()); err != nil {
		t.Fatal(err)
	}
	if uploadCount != 1 {
		t.Fatalf("expected one platform avatar upload, got %d", uploadCount)
	}
	if len(roomAvatarURLs) != 2 || roomAvatarURLs[0] != "mxc://local/platform-whatsapp" || roomAvatarURLs[1] != "mxc://local/platform-whatsapp" {
		t.Fatalf("expected both rooms to use cached mxc, got %#v", roomAvatarURLs)
	}
}

func TestMatrixClientSinkCreatesServiceSpacesAndLinksPortals(t *testing.T) {
	var createdSpaces []string
	var spaceChildLinks []string
	var spaceParentLinks []string
	uploadCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/logo"})
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/createRoom"):
			var body struct {
				Name            string         `json:"name"`
				CreationContent map[string]any `json:"creation_content"`
				InitialState    []struct {
					Type string `json:"type"`
				} `json:"initial_state"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create room body: %v", err)
			}
			if body.CreationContent["type"] != "m.space" {
				t.Fatalf("expected m.space creation for %q, got %#v", body.Name, body.CreationContent)
			}
			createdSpaces = append(createdSpaces, body.Name)
			roomID := "!space-root:local"
			if body.Name == "WhatsApp" {
				roomID = "!space-whatsapp:local"
			}
			if body.Name == "Signal" {
				roomID = "!space-signal:local"
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"room_id": roomID})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.space.child/"):
			spaceChildLinks = append(spaceChildLinks, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$child:local"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.space.parent/"):
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode space parent body: %v", err)
			}
			if len(body) > 0 {
				spaceParentLinks = append(spaceParentLinks, r.URL.Path)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$parent:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	wa := Chat{ID: "!wa:beeper", AccountID: "whatsapp", Network: "WhatsApp", Name: "Family"}
	sig := Chat{ID: "!sig:beeper", AccountID: "signal", Network: "Signal", Name: "Friends"}
	if err := store.UpsertPortal(ctx, wa, "!portal-wa:local", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPortal(ctx, sig, "!portal-sig:local", ""); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	if err := sink.EnsurePortalSpaces(ctx, []Chat{wa, sig}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(createdSpaces, ",") != "All Chats,Signal,WhatsApp" {
		t.Fatalf("unexpected space creation order/names: %#v", createdSpaces)
	}
	if uploadCount != 3 {
		t.Fatalf("expected root and platform space logos to upload, got %d", uploadCount)
	}
	for _, want := range []string{"!space-signal:local", "!space-whatsapp:local", "!portal-sig:local", "!portal-wa:local"} {
		if !pathsContainEscapedRoomID(spaceChildLinks, want) {
			t.Fatalf("expected m.space.child link for %s in %#v", want, spaceChildLinks)
		}
	}
	for _, want := range []string{"!space-signal:local", "!space-whatsapp:local"} {
		if !pathsContainEscapedRoomID(spaceParentLinks, want) {
			t.Fatalf("expected m.space.parent link for %s in %#v", want, spaceParentLinks)
		}
	}
}

func TestMatrixClientSinkCanCreatePlatformAccountSpaceHierarchy(t *testing.T) {
	var createdSpaces []string
	var spaceChildLinks []string
	var uploadCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/createRoom"):
			var body struct {
				Name string `json:"name"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode create room: %v", err)
			}
			createdSpaces = append(createdSpaces, body.Name)
			roomID := "!space-" + strings.ToLower(strings.NewReplacer(" ", "-", "·", "").Replace(body.Name)) + ":local"
			_ = json.NewEncoder(w).Encode(map[string]string{"room_id": roomID})
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": fmt.Sprintf("mxc://local/upload-%d", uploadCount)})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.space.child/"):
			spaceChildLinks = append(spaceChildLinks, r.URL.Path)
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$child:local"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.space.parent/"):
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$parent:local"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room."):
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$state:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	wa := Chat{ID: "!wa:beeper", AccountID: "local-whatsapp_main", Network: "WhatsApp", Name: "Family"}
	sig := Chat{ID: "!sig:beeper", AccountID: "local-signal_support", Network: "Signal", Name: "Support"}
	if err := store.UpsertPortal(ctx, wa, "!portal-wa:local", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertPortal(ctx, sig, "!portal-sig:local", ""); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	cfg.Matrix.SpaceGrouping = "platform-account"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	if err := sink.EnsurePortalSpaces(ctx, []Chat{wa, sig}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(createdSpaces, ",")
	for _, want := range []string{"All Chats", "Signal", "Signal · Support", "WhatsApp", "WhatsApp · Main"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected created spaces to include %q, got %#v", want, createdSpaces)
		}
	}
	if uploadCount != 5 {
		t.Fatalf("expected root, platform, and account space avatars to upload, got %d", uploadCount)
	}
	for _, want := range []string{"!portal-wa:local", "!portal-sig:local"} {
		if !pathsContainEscapedRoomID(spaceChildLinks, want) {
			t.Fatalf("expected room child link for %s in %#v", want, spaceChildLinks)
		}
	}
}

func TestMatrixClientSinkReturnsRateLimitRetryAfterForCreateRoom(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || !strings.HasSuffix(r.URL.Path, "/createRoom") {
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"errcode":        "M_LIMIT_EXCEEDED",
			"error":          "Too Many Requests",
			"retry_after_ms": 37,
		})
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sink.EnsurePortal(ctx, Chat{ID: "!limited:beeper", AccountID: "whatsapp", Name: "Limited"}, nil)
	if err == nil {
		t.Fatal("expected rate-limit error")
	}
	var rateErr *MatrixRateLimitError
	if !errors.As(err, &rateErr) {
		t.Fatalf("expected MatrixRateLimitError, got %T: %v", err, err)
	}
	if rateErr.RetryAfter != 37*time.Millisecond {
		t.Fatalf("expected retry_after_ms to be preserved, got %s", rateErr.RetryAfter)
	}
	if rateErr.StatusCode != http.StatusTooManyRequests || rateErr.ErrCode != "M_LIMIT_EXCEEDED" {
		t.Fatalf("unexpected rate-limit metadata: %#v", rateErr)
	}
}

func TestMatrixClientSinkUpdatesExistingRoomAvatar(t *testing.T) {
	var stateAvatarURL string
	var sawName bool
	var sawTopic bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			if ct := r.Header.Get("Content-Type"); ct != "image/png" {
				t.Fatalf("unexpected upload content-type %q", ct)
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/existing-avatar"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room.avatar/"):
			var body struct {
				URL string `json:"url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode state body: %v", err)
			}
			stateAvatarURL = body.URL
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$avatar:local"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room.name/"):
			sawName = true
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$name:local"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room.topic/"):
			sawTopic = true
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$topic:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertPortal(ctx, Chat{ID: "!chat:beeper"}, "!existing:local", ""); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	roomID, err := sink.EnsurePortal(ctx, Chat{ID: "!chat:beeper", AccountID: "signal", Name: "Existing"}, &MatrixMedia{
		Content:   bytes.NewReader([]byte("avatar")),
		FileName:  "avatar.png",
		MimeType:  "image/png",
		SizeBytes: 6,
	})
	if err != nil {
		t.Fatal(err)
	}
	if roomID != "!existing:local" {
		t.Fatalf("unexpected room ID %q", roomID)
	}
	if stateAvatarURL != "mxc://local/existing-avatar" {
		t.Fatalf("expected avatar state update, got %q", stateAvatarURL)
	}
	if !sawName || !sawTopic {
		t.Fatalf("expected existing room name/topic refresh, name=%v topic=%v", sawName, sawTopic)
	}
}

func TestMatrixClientSinkReuploadsAvatarWhenContentHashChanges(t *testing.T) {
	var uploadCount int
	var stateAvatarURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/new-avatar"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room.avatar/"):
			var body struct {
				URL string `json:"url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode state body: %v", err)
			}
			stateAvatarURL = body.URL
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$avatar:local"})
		case r.Method == http.MethodPut && (strings.Contains(r.URL.Path, "/state/m.room.name/") || strings.Contains(r.URL.Path, "/state/m.room.topic/")):
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$state:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!chat:beeper", AccountID: "signal", Name: "Existing"}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMediaCache(ctx, MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "old-hash",
		MimeType:    "image/png",
		SizeBytes:   3,
	}, "mxc://local/old-avatar"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sink.EnsurePortal(ctx, chat, &MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "new-hash",
		Content:     bytes.NewReader([]byte("new")),
		FileName:    "avatar.png",
		MimeType:    "image/png",
		SizeBytes:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if uploadCount != 1 {
		t.Fatalf("expected changed avatar hash to upload once, got %d", uploadCount)
	}
	if stateAvatarURL != "mxc://local/new-avatar" {
		t.Fatalf("expected new avatar state, got %q", stateAvatarURL)
	}
}

func TestMatrixClientSinkReusesAvatarCacheWhenContentHashMatches(t *testing.T) {
	var uploadCount int
	var stateAvatarURL string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			uploadCount++
			_ = json.NewEncoder(w).Encode(map[string]string{"content_uri": "mxc://local/should-not-upload"})
		case r.Method == http.MethodPut && strings.Contains(r.URL.Path, "/state/m.room.avatar/"):
			var body struct {
				URL string `json:"url"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("decode state body: %v", err)
			}
			stateAvatarURL = body.URL
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$avatar:local"})
		case r.Method == http.MethodPut && (strings.Contains(r.URL.Path, "/state/m.room.name/") || strings.Contains(r.URL.Path, "/state/m.room.topic/")):
			_ = json.NewEncoder(w).Encode(map[string]string{"event_id": "$state:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!chat:beeper", AccountID: "signal", Name: "Existing"}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMediaCache(ctx, MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "same-hash",
		MimeType:    "image/png",
		SizeBytes:   3,
	}, "mxc://local/cached-avatar"); err != nil {
		t.Fatal(err)
	}
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sink.EnsurePortal(ctx, chat, &MatrixMedia{
		AssetID:     "avatar-asset",
		ContentHash: "same-hash",
		Content:     bytes.NewReader([]byte("new")),
		FileName:    "avatar.png",
		MimeType:    "image/png",
		SizeBytes:   3,
	})
	if err != nil {
		t.Fatal(err)
	}
	if uploadCount != 0 {
		t.Fatalf("expected matching avatar hash to reuse cache, got %d uploads", uploadCount)
	}
	if stateAvatarURL != "mxc://local/cached-avatar" {
		t.Fatalf("expected cached avatar state, got %q", stateAvatarURL)
	}
}

func TestMatrixClientSinkDoesNotCreateRoomWhenAvatarUploadFails(t *testing.T) {
	var createdRoom bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.Contains(r.URL.Path, "/upload"):
			http.Error(w, "upload failed", http.StatusBadGateway)
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/createRoom"):
			createdRoom = true
			_ = json.NewEncoder(w).Encode(map[string]string{"room_id": "!should-not-exist:local"})
		default:
			t.Fatalf("unexpected Matrix request %s %s", r.Method, r.URL.Path)
		}
	}))
	defer server.Close()

	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, store, "token")
	if err != nil {
		t.Fatal(err)
	}

	_, err = sink.EnsurePortal(ctx, Chat{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Needs Avatar"}, &MatrixMedia{
		Content:   bytes.NewReader([]byte("avatar")),
		FileName:  "avatar.png",
		MimeType:  "image/png",
		SizeBytes: 6,
	})
	if err == nil {
		t.Fatal("expected avatar upload error")
	}
	if createdRoom {
		t.Fatal("room should not be created after avatar upload failure")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func pathsContainEscapedRoomID(paths []string, roomID string) bool {
	escaped := strings.ReplaceAll(roomID, "!", "%21")
	escaped = strings.ReplaceAll(escaped, ":", "%3A")
	for _, path := range paths {
		if strings.Contains(path, escaped) || strings.Contains(path, roomID) {
			return true
		}
	}
	return false
}
