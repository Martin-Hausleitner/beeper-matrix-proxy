package beepersource

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMatrixSyncForwardsCinnyMessageToBeeper(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!chat:beeper", AccountID: "signal", Name: "Test"}
	if err := store.UpsertPortal(ctx, chat, "!room:local", "beeper-cursor"); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/sync" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"next_batch": "sync-2",
			"rooms": {
				"join": {
					"!room:local": {
						"timeline": {
							"events": [{
								"type": "m.room.message",
								"event_id": "$cinny1:local",
								"sender": "@cinny:local",
								"content": {
									"msgtype": "m.text",
									"body": "hello from cinny"
								}
							}]
						}
					}
				}
			}
		}`))
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@bridge:local"
	cfg.Matrix.InsecureSkipTLS = true
	source := NewMatrixClientSource(cfg, store, "matrix-token")
	handled, err := source.SyncOnce(ctx, svc)
	if err != nil {
		t.Fatalf("SyncOnce returned error: %v", err)
	}
	if handled != 1 {
		t.Fatalf("expected one handled Matrix event, got %d", handled)
	}
	if len(api.sent) != 1 {
		t.Fatalf("expected one Beeper send, got %d", len(api.sent))
	}
	if api.sent[0].ChatID != chat.ID || api.sent[0].Text != "hello from cinny" {
		t.Fatalf("unexpected outbound: %#v", api.sent[0])
	}
	token, err := store.GetValue(ctx, "matrix_sync_since")
	if err != nil {
		t.Fatal(err)
	}
	if token != "sync-2" {
		t.Fatalf("expected sync token to be stored, got %q", token)
	}
}

func TestMatrixSyncIgnoresVoiceAIGeneratedReplies(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!chat:beeper", AccountID: "signal", Name: "Test"}
	if err := store.UpsertPortal(ctx, chat, "!room:local", "beeper-cursor"); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/_matrix/client/v3/sync" {
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"next_batch": "sync-voice-ai",
			"rooms": {
				"join": {
					"!room:local": {
						"timeline": {
							"events": [{
								"type": "m.room.message",
								"event_id": "$voice-ai:local",
								"sender": "@summary:local",
								"content": {
									"msgtype": "m.notice",
									"body": "Sprachnachricht transkribiert",
									"com.openclaw.voice_ai": {
										"source_message_id": "$voice1",
										"source_event_id": "$event-voice1:local",
										"chat_id": "!chat:beeper",
										"kind": "transcript_summary"
									}
								}
							}]
						}
					}
				}
			}
		}`))
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@bridge:local"
	cfg.Matrix.InsecureSkipTLS = true
	source := NewMatrixClientSource(cfg, store, "matrix-token")
	handled, err := source.SyncOnce(ctx, svc)
	if err != nil {
		t.Fatalf("SyncOnce returned error: %v", err)
	}
	if handled != 0 {
		t.Fatalf("expected voice AI reply to be ignored, got %d handled events", handled)
	}
	if len(api.sent) != 0 {
		t.Fatalf("expected no Beeper send for generated voice AI reply, got %d", len(api.sent))
	}
}

func TestMatrixSyncForwardsMediaToBeeper(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertPortal(ctx, Chat{ID: "!chat:beeper"}, "!room:local", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/_matrix/client/v3/sync":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{
				"next_batch": "sync-media",
				"rooms": {"join": {"!room:local": {"timeline": {"events": [{
					"type": "m.room.message",
					"event_id": "$media:local",
					"sender": "@cinny:local",
					"content": {
						"msgtype": "m.image",
						"body": "cinny.png",
						"url": "mxc://local.test/media123",
						"info": {"mimetype": "image/png", "size": 7, "w": 2, "h": 3}
					}
				}]}}}}
			}`))
		case "/_matrix/client/v1/media/download/local.test/media123":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("pngdata"))
		default:
			t.Fatalf("unexpected path %s", r.URL.Path)
		}
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@bridge:local"
	cfg.Matrix.InsecureSkipTLS = true
	source := NewMatrixClientSource(cfg, store, "matrix-token")
	handled, err := source.SyncOnce(ctx, svc)
	if err != nil {
		t.Fatalf("SyncOnce returned error: %v", err)
	}
	if handled != 1 {
		t.Fatalf("expected one handled media event, got %d", handled)
	}
	if len(api.sent) != 1 {
		t.Fatalf("expected one Beeper send, got %d", len(api.sent))
	}
	if api.sent[0].Attachment == nil {
		t.Fatalf("expected outbound attachment, got %#v", api.sent[0])
	}
	if api.sent[0].Attachment.FileName != "cinny.png" || api.sent[0].Attachment.MimeType != "image/png" {
		t.Fatalf("unexpected attachment: %#v", api.sent[0].Attachment)
	}
}

func TestMatrixSyncForwardsEditDeleteAndReactionToBeeper(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertPortal(ctx, Chat{ID: "!chat:beeper"}, "!room:local", ""); err != nil {
		t.Fatal(err)
	}
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-target",
		MatrixEventID:   "$matrix-target:local",
		ChatID:          "!chat:beeper",
		Version:         "1",
	}); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"next_batch": "sync-relations",
			"rooms": {"join": {"!room:local": {"timeline": {"events": [
				{
					"type": "m.room.message",
					"event_id": "$edit:local",
					"sender": "@cinny:local",
					"content": {
						"msgtype": "m.text",
						"body": "* edited body",
						"m.new_content": {"msgtype": "m.text", "body": "edited body"},
						"m.relates_to": {"rel_type": "m.replace", "event_id": "$matrix-target:local"}
					}
				},
				{
					"type": "m.reaction",
					"event_id": "$reaction:local",
					"sender": "@cinny:local",
					"content": {
						"m.relates_to": {"rel_type": "m.annotation", "event_id": "$matrix-target:local", "key": "✅"}
					}
				},
				{
					"type": "m.room.redaction",
					"event_id": "$redaction:local",
					"sender": "@cinny:local",
					"redacts": "$matrix-target:local",
					"content": {}
				}
			]}}}}
		}`))
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@bridge:local"
	cfg.Matrix.InsecureSkipTLS = true
	source := NewMatrixClientSource(cfg, store, "matrix-token")
	handled, err := source.SyncOnce(ctx, svc)
	if err != nil {
		t.Fatalf("SyncOnce returned error: %v", err)
	}
	if handled != 3 {
		t.Fatalf("expected three handled relation events, got %d", handled)
	}
	if len(api.updates) != 1 || api.updates[0].Text != "edited body" {
		t.Fatalf("expected edit update, got %#v", api.updates)
	}
	if len(api.reactions) != 1 || !strings.Contains(api.reactions[0], "✅") {
		t.Fatalf("expected reaction add, got %#v", api.reactions)
	}
	if len(api.deletes) != 1 || api.deletes[0] != "$beeper-target" {
		t.Fatalf("expected delete target, got %#v", api.deletes)
	}
}

func TestMatrixSyncIgnoresBridgeEchoes(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertPortal(ctx, Chat{ID: "!chat:beeper"}, "!room:local", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"next_batch": "sync-3",
			"rooms": {
				"join": {
					"!room:local": {
						"timeline": {
							"events": [{
								"type": "m.room.message",
								"event_id": "$echo:local",
								"sender": "@bridge:local",
								"content": {"msgtype": "m.text", "body": "mirrored"}
							}]
						}
					}
				}
			}
		}`))
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@bridge:local"
	cfg.Matrix.InsecureSkipTLS = true
	source := NewMatrixClientSource(cfg, store, "matrix-token")
	if _, err := source.SyncOnce(ctx, svc); err != nil {
		t.Fatalf("SyncOnce returned error: %v", err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("expected bridge echo to be ignored, got %#v", api.sent)
	}
}
