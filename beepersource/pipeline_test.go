package beepersource

import (
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReconcileMirrorsTextMessagesAndStoresMapping(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "local-whatsapp",
			Name:      "Test Chat",
		}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$m1",
				ChatID:    "!chat:beeper",
				SenderID:  "@alice:local-whatsapp.localhost",
				Type:      MessageTypeText,
				Text:      "hello",
				Timestamp: time.Unix(100, 0).UTC(),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.events) != 1 {
		t.Fatalf("expected one Matrix event, got %d", len(matrix.events))
	}
	if matrix.events[0].Body != "hello" {
		t.Fatalf("unexpected event body %q", matrix.events[0].Body)
	}

	got, ok, err := store.MessageByBeeperID(ctx, "$m1")
	if err != nil || !ok {
		t.Fatalf("expected message mapping, ok=%v err=%v", ok, err)
	}
	if got.MatrixEventID == "" {
		t.Fatal("expected Matrix event ID to be stored")
	}
}

func TestReconcileIsIdempotentWhenMessageMappingExists(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "local-signal", Name: "Idempotent"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$m1",
				ChatID:    "!chat:beeper",
				SenderID:  "@bob:local-signal.localhost",
				Type:      MessageTypeText,
				Text:      "once",
				Timestamp: time.Unix(100, 0).UTC(),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.events) != 1 {
		t.Fatalf("expected idempotent reconcile to send one event, got %d", len(matrix.events))
	}
}

func TestBackfillHistoryPaginatesOlderMessages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!chat:beeper", AccountID: "local-signal", Name: "History"}
	api := &fakeBeeperAPI{
		chats: []Chat{chat},
		messagePages: map[string][]MessagePage{
			chat.ID: {{
				Messages: []Message{{
					ID:        "$newer",
					ChatID:    chat.ID,
					SenderID:  "@bob:local-signal.localhost",
					Type:      MessageTypeText,
					Text:      "newer",
					Timestamp: time.Unix(200, 0).UTC(),
				}},
				OldestCursor: "older-page",
				NewestCursor: "newest",
				HasMore:      true,
			}, {
				Messages: []Message{{
					ID:        "$older",
					ChatID:    chat.ID,
					SenderID:  "@bob:local-signal.localhost",
					Type:      MessageTypeText,
					Text:      "older",
					Timestamp: time.Unix(100, 0).UTC(),
				}},
				OldestCursor: "",
				HasMore:      false,
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	result, err := svc.BackfillHistoryOnce(ctx, 1)
	if err != nil {
		t.Fatalf("first backfill returned error: %v", err)
	}
	if result.ChatsProcessed != 1 || result.MessagesSeen != 1 || result.CompletedChats != 0 {
		t.Fatalf("unexpected first result: %#v", result)
	}
	result, err = svc.BackfillHistoryOnce(ctx, 1)
	if err != nil {
		t.Fatalf("second backfill returned error: %v", err)
	}
	if result.ChatsProcessed != 1 || result.MessagesSeen != 1 || result.CompletedChats != 1 {
		t.Fatalf("unexpected second result: %#v", result)
	}
	if len(matrix.events) != 2 {
		t.Fatalf("expected two mirrored events, got %d", len(matrix.events))
	}
	done, err := store.PortalBackfillDone(ctx, chat.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !done {
		t.Fatal("expected chat backfill to be marked done")
	}
}

func TestReconcileMirrorsImageAttachmentAsMatrixMedia(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Media Test"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$img1",
				ChatID:    "!chat:beeper",
				SenderID:  "@alice:local-whatsapp.localhost",
				Type:      MessageTypeImage,
				Text:      "image caption",
				Timestamp: time.Unix(100, 0).UTC(),
				Attachments: []Attachment{{
					URL:       "localmxc://image",
					FileName:  "image.png",
					MimeType:  "image/png",
					SizeBytes: 7,
					Width:     2,
					Height:    3,
				}},
			}},
		},
		assets: map[string]string{"localmxc://image": "pngdata"},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.events) != 1 {
		t.Fatalf("expected one Matrix event, got %d", len(matrix.events))
	}
	if matrix.events[0].Media == nil {
		t.Fatal("expected Matrix media to be attached")
	}
	if matrix.events[0].Media.FileName != "image.png" || matrix.events[0].Media.MimeType != "image/png" {
		t.Fatalf("unexpected media metadata: %#v", matrix.events[0].Media)
	}
	body, err := io.ReadAll(matrix.events[0].Media.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "pngdata" {
		t.Fatalf("unexpected media body %q", string(body))
	}
}

func TestReconcileMirrorsEveryBeeperAttachmentAsMatrixMedia(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Multi Media"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$multi",
				ChatID:    "!chat:beeper",
				SenderID:  "@alice:local-whatsapp.localhost",
				Type:      MessageTypeImage,
				Text:      "album",
				Timestamp: time.Unix(100, 0).UTC(),
				Attachments: []Attachment{{
					ID:        "att-1",
					URL:       "localmxc://one",
					FileName:  "one.png",
					MimeType:  "image/png",
					SizeBytes: 3,
				}, {
					ID:        "att-2",
					URL:       "localmxc://two",
					FileName:  "two.jpg",
					MimeType:  "image/jpeg",
					SizeBytes: 3,
				}},
				RawJSON: `{"id":"$multi","attachments":[{"id":"att-1"},{"id":"att-2"}]}`,
			}},
		},
		assets: map[string]string{
			"localmxc://one": "one",
			"localmxc://two": "two",
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.events) != 2 {
		t.Fatalf("expected one Matrix media event per Beeper attachment, got %d", len(matrix.events))
	}
	if matrix.events[0].Media == nil || matrix.events[0].Media.FileName != "one.png" {
		t.Fatalf("unexpected first media event: %#v", matrix.events[0])
	}
	if matrix.events[1].Media == nil || matrix.events[1].Media.FileName != "two.jpg" {
		t.Fatalf("unexpected second media event: %#v", matrix.events[1])
	}
	if _, ok, err := store.MessageByBeeperID(ctx, "$multi/attachment/1"); err != nil || !ok {
		t.Fatalf("expected second attachment mapping, ok=%v err=%v", ok, err)
	}
	raw, ok, err := store.BeeperMessageRaw(ctx, "$multi")
	if err != nil || !ok {
		t.Fatalf("expected raw Beeper message to be stored, ok=%v err=%v", ok, err)
	}
	if !strings.Contains(raw, `"att-2"`) {
		t.Fatalf("expected raw metadata to include every attachment, got %s", raw)
	}
}

func TestReconcileMapsBeeperLinkedMessageToMatrixReply(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-parent",
		MatrixEventID:   "$matrix-parent:local",
		ChatID:          "!chat:beeper",
		Version:         "parent",
	}); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "signal", Name: "Reply Test"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:              "$beeper-reply",
				ChatID:          "!chat:beeper",
				SenderID:        "@alice:signal",
				Type:            MessageTypeText,
				Text:            "reply from beeper",
				LinkedMessageID: "$beeper-parent",
				Timestamp:       time.Unix(100, 0).UTC(),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.events) != 1 {
		t.Fatalf("expected one Matrix event, got %d", len(matrix.events))
	}
	if matrix.events[0].ReplyToEvent != "$matrix-parent:local" {
		t.Fatalf("expected Matrix reply target to be remapped, got %q", matrix.events[0].ReplyToEvent)
	}
}

func TestReconcileDownloadsChatAvatarForNewPortal(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "whatsapp",
			Name:      "Avatar Test",
			AvatarURL: "localmxc://avatar",
		}},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"localmxc://avatar": "avatar-bytes"},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected one portal avatar, got %d", len(matrix.avatars))
	}
	body, err := io.ReadAll(matrix.avatars[0].Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "avatar-bytes" {
		t.Fatalf("unexpected avatar body %q", string(body))
	}
}

func TestReconcileResolvesRelativeBeeperAvatarURL(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "telegram",
			Name:      "Relative Avatar",
			AvatarURL: "/_matrix/media/v3/download/beeper/avatar",
		}},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"http://localhost:23373/_matrix/media/v3/download/beeper/avatar": "avatar-bytes"},
	}
	matrix := &fakeMatrixSink{}
	cfg := DefaultConfig()
	cfg.Beeper.BaseURL = "http://localhost:23373"
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(api.downloadedAssets) != 1 || api.downloadedAssets[0] != "http://localhost:23373/_matrix/media/v3/download/beeper/avatar" {
		t.Fatalf("expected resolved Beeper avatar URL, got %#v", api.downloadedAssets)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected one portal avatar, got %d", len(matrix.avatars))
	}
}

func TestResolveBeeperAssetURLKeepsAbsoluteLocalPaths(t *testing.T) {
	got := resolveBeeperAssetURL("http://localhost:23373", "/Users/mh/Library/Application%20Support/BeeperTexts/media/avatar.jpg")

	if got != "/Users/mh/Library/Application%20Support/BeeperTexts/media/avatar.jpg" {
		t.Fatalf("expected absolute local path to stay local, got %q", got)
	}
}

func TestLocalAvatarMediaReadsEscapedLocalPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "avatar file.png")
	if err := os.WriteFile(path, []byte("png-ish"), 0o600); err != nil {
		t.Fatal(err)
	}
	escaped := strings.ReplaceAll(path, " ", "%20")

	avatar, ok, err := localAvatarMedia(escaped)
	if err != nil || !ok {
		t.Fatalf("expected escaped local path avatar, ok=%v err=%v", ok, err)
	}
	defer avatar.Content.(io.Closer).Close()
	body, err := io.ReadAll(avatar.Content)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "png-ish" {
		t.Fatalf("unexpected avatar body %q", string(body))
	}
}

func TestReconcilePortalsOnlyCreatesRoomsWithoutMessages(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "whatsapp",
			Network:   "WhatsApp",
			Name:      "Family",
		}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:     "$m1",
				ChatID: "!chat:beeper",
				Type:   MessageTypeText,
				Text:   "should not import",
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected platform avatar fallback, got %d", len(matrix.avatars))
	}
	if len(matrix.events) != 0 {
		t.Fatalf("expected no message imports, got %#v", matrix.events)
	}
	roomID, ok, err := store.PortalRoomID(ctx, "!chat:beeper")
	if err != nil || !ok || roomID == "" {
		t.Fatalf("expected portal mapping, roomID=%q ok=%v err=%v", roomID, ok, err)
	}
}

func TestReconcilePortalsOnlyRefreshesExistingPortalProfiles(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{
		ID:        "!chat:beeper",
		AccountID: "telegram",
		Network:   "Telegram",
		Name:      "Existing Chat",
		AvatarURL: "localmxc://avatar-v2",
	}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", "cursor"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetValue(ctx, portalProfileSyncKey(chat.ID), "stale-profile"); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{
		chats:    []Chat{chat},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"localmxc://avatar-v2": "avatar-v2"},
	}
	matrix := &fakeMatrixSink{}
	cfg := DefaultConfig()
	cfg.Matrix.RoomNamePrefix = ""
	cfg.Matrix.RoomNameIncludePlatform = false
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if matrix.portalAttempts[chat.ID] != 1 {
		t.Fatalf("expected existing portal to be refreshed once, got %d attempts", matrix.portalAttempts[chat.ID])
	}
	if len(matrix.events) != 0 {
		t.Fatalf("expected no message imports, got %#v", matrix.events)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected existing portal avatar refresh, got %d avatars", len(matrix.avatars))
	}
}

func TestReconcileCanPreferPlatformAvatarsOverChatAvatars(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "whatsapp",
			Network:   "WhatsApp",
			Name:      "Has Real Avatar",
			AvatarURL: "localmxc://real-avatar",
		}},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"localmxc://real-avatar": "real-avatar"},
	}
	matrix := &fakeMatrixSink{}
	cfg := DefaultConfig()
	cfg.Matrix.PlatformAvatars = true
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected one platform avatar, got %d", len(matrix.avatars))
	}
	if matrix.avatars[0].MimeType != "image/png" {
		t.Fatalf("expected platform PNG avatar, got %#v", matrix.avatars[0])
	}
}

func TestReconcileCanForceAvatarRefreshWhenSyncValueMatches(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{
		ID:        "!chat:beeper",
		AccountID: "signal",
		Network:   "Signal",
		Name:      "Force Avatar",
		AvatarURL: "localmxc://same-avatar",
	}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", "cursor"); err != nil {
		t.Fatal(err)
	}
	if err := store.SetValue(ctx, portalAvatarSyncKey(chat.ID), "localmxc://same-avatar"); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{
		chats:    []Chat{chat},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"localmxc://same-avatar": "avatar"},
	}
	matrix := &fakeMatrixSink{}
	cfg := DefaultConfig()
	cfg.Matrix.ForceAvatarSync = true
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected forced avatar refresh, got %d avatars", len(matrix.avatars))
	}
}

func TestReconcilePortalsOnlySkipsArchivedChatsByDefault(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:         "!archived:beeper",
			AccountID:  "whatsapp",
			Network:    "WhatsApp",
			Name:       "Archived",
			IsArchived: true,
		}},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if len(matrix.avatars) != 0 {
		t.Fatalf("expected archived chat to be skipped, got %d avatars", len(matrix.avatars))
	}
	if roomID, ok, err := store.PortalRoomID(ctx, "!archived:beeper"); err != nil || ok || roomID != "" {
		t.Fatalf("expected no archived portal mapping, roomID=%q ok=%v err=%v", roomID, ok, err)
	}
}

func TestReconcilePortalsOnlyContinuesAfterPortalError(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{
			{ID: "!bad:beeper", AccountID: "signal", Name: "Bad"},
			{ID: "!good:beeper", AccountID: "signal", Name: "Good"},
		},
	}
	matrix := &fakeMatrixSink{failPortals: map[string]error{"!bad:beeper": errors.New("boom")}}
	svc := NewService(DefaultConfig(), store, api, matrix)

	err := svc.ReconcilePortalsOnly(ctx)
	if err == nil {
		t.Fatal("expected portal error to be reported")
	}
	if _, ok, lookupErr := store.PortalRoomID(ctx, "!good:beeper"); lookupErr != nil || !ok {
		t.Fatalf("expected good portal to still be stored, ok=%v err=%v", ok, lookupErr)
	}
}

func TestReconcilePortalsOnlyRetriesRateLimitedPortalWithoutFailingImport(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{
			{ID: "!limited:beeper", AccountID: "signal", Name: "Limited"},
			{ID: "!good:beeper", AccountID: "signal", Name: "Good"},
		},
	}
	matrix := &fakeMatrixSink{
		failPortalSequences: map[string][]error{
			"!limited:beeper": {&MatrixRateLimitError{RetryAfter: time.Millisecond, StatusCode: http.StatusTooManyRequests, ErrCode: "M_LIMIT_EXCEEDED"}},
		},
	}
	cfg := DefaultConfig()
	cfg.Sync.PortalWorkers = 2
	cfg.Sync.PortalTimeoutSeconds = 2
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error after temporary 429: %v", err)
	}
	if got := matrix.portalAttempts["!limited:beeper"]; got != 2 {
		t.Fatalf("expected rate-limited portal to be retried once, got %d attempts", got)
	}
	for _, chatID := range []string{"!limited:beeper", "!good:beeper"} {
		if _, ok, err := store.PortalRoomID(ctx, chatID); err != nil || !ok {
			t.Fatalf("expected portal %s to be stored after import, ok=%v err=%v", chatID, ok, err)
		}
	}
}

func TestReconcilePortalsOnlyRecreatesInaccessibleExistingPortal(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!stale:beeper", AccountID: "telegram", Network: "Telegram", Name: "Stale"}
	if err := store.UpsertPortal(ctx, chat, "!old:local", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{chats: []Chat{chat}}
	matrix := &fakeMatrixSink{inaccessibleRooms: map[string]bool{"!old:local": true}}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	roomID, ok, err := store.PortalRoomID(ctx, chat.ID)
	if err != nil || !ok {
		t.Fatalf("expected recreated portal mapping, ok=%v err=%v", ok, err)
	}
	if roomID == "!old:local" {
		t.Fatalf("expected stale room to be replaced, still got %q", roomID)
	}
	if got := matrix.portalAttempts[chat.ID]; got != 1 {
		t.Fatalf("expected inaccessible portal to be recreated once, got %d attempts", got)
	}
}

func TestReconcilePortalsOnlyRefreshesWhenAccessibilityCheckFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!check-fails:beeper", AccountID: "telegram", Network: "Telegram", Name: "Check Fails"}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{chats: []Chat{chat}}
	matrix := &fakeMatrixSink{accessErrs: map[string]error{"!existing:local": errors.New("matrix state check timed out")}}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if got := matrix.portalAttempts[chat.ID]; got != 1 {
		t.Fatalf("expected existing portal to still be refreshed once, got %d attempts", got)
	}
}

func TestReconcilePortalsOnlyCanSkipAccessibilityChecks(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{ID: "!skip-check:beeper", AccountID: "telegram", Network: "Telegram", Name: "Skip Check"}
	if err := store.UpsertPortal(ctx, chat, "!existing:local", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{chats: []Chat{chat}}
	matrix := &fakeMatrixSink{accessErrs: map[string]error{"!existing:local": errors.New("should not be called")}}
	cfg := DefaultConfig()
	cfg.Sync.PortalCheckAccess = false
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if got := matrix.portalAttempts[chat.ID]; got != 1 {
		t.Fatalf("expected existing portal to be refreshed once, got %d attempts", got)
	}
}

func TestReconcilePortalsOnlyOrganizesExistingPortalsIntoSpaces(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chats := []Chat{
		{ID: "!wa:beeper", AccountID: "whatsapp", Network: "WhatsApp", Name: "WA"},
		{ID: "!sig:beeper", AccountID: "signal", Network: "Signal", Name: "Signal"},
	}
	for _, chat := range chats {
		if err := store.UpsertPortal(ctx, chat, "!matrix-"+chat.ID, ""); err != nil {
			t.Fatal(err)
		}
	}
	api := &fakeBeeperAPI{chats: chats}
	matrix := &fakeMatrixSink{}
	cfg := DefaultConfig()
	cfg.Matrix.Spaces = true
	svc := NewService(cfg, store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatalf("ReconcilePortalsOnly returned error: %v", err)
	}
	if got := len(matrix.spaceChats); got != 2 {
		t.Fatalf("expected existing portals to be organized into spaces, got %d chats", got)
	}
}

func TestReconcileRefreshesExistingPortalAvatarWhenChanged(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertPortal(ctx, Chat{ID: "!chat:beeper"}, "!matrix-!chat:beeper", ""); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{
		chats: []Chat{{
			ID:        "!chat:beeper",
			AccountID: "signal",
			Name:      "Existing Avatar Test",
			AvatarURL: "localmxc://avatar-v2",
		}},
		messages: map[string][]Message{"!chat:beeper": nil},
		assets:   map[string]string{"localmxc://avatar-v2": "avatar-v2"},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.avatars) != 1 {
		t.Fatalf("expected existing portal avatar refresh, got %d avatars", len(matrix.avatars))
	}
}

func TestReconcileFallsBackToNoticeWhenMatrixMediaUploadFails(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Media Test"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$img1",
				ChatID:    "!chat:beeper",
				SenderID:  "@alice:local-whatsapp.localhost",
				Type:      MessageTypeImage,
				Timestamp: time.Unix(100, 0).UTC(),
				Attachments: []Attachment{{
					URL:       "localmxc://image",
					FileName:  "image.png",
					MimeType:  "image/png",
					SizeBytes: 7,
				}},
			}},
		},
		assets: map[string]string{"localmxc://image": "pngdata"},
	}
	matrix := &fakeMatrixSink{failMedia: true}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatalf("ReconcileOnce returned error: %v", err)
	}
	if len(matrix.events) != 2 {
		t.Fatalf("expected media attempt and fallback notice, got %d events", len(matrix.events))
	}
	if matrix.events[1].MsgType != "m.notice" || !strings.Contains(matrix.events[1].Body, "could not be mirrored") {
		t.Fatalf("unexpected fallback event: %#v", matrix.events[1])
	}
}

func TestMatrixToBeeperHonorsKillSwitch(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	cfg := DefaultConfig()
	cfg.Safety.DisableMatrixToBeeper = true
	api := &fakeBeeperAPI{}
	svc := NewService(cfg, store, api, &fakeMatrixSink{})

	err := svc.HandleMatrixMessage(ctx, MatrixInbound{
		ChatID: "!chat:beeper",
		Body:   "blocked",
	})

	if err != ErrMatrixToBeeperDisabled {
		t.Fatalf("expected kill switch error, got %v", err)
	}
	if len(api.sent) != 0 {
		t.Fatalf("expected no Beeper sends, got %#v", api.sent)
	}
}

func TestMatrixToBeeperUsesDeterministicEchoKey(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})

	err := svc.HandleMatrixMessage(ctx, MatrixInbound{
		ChatID:        "!chat:beeper",
		MatrixEventID: "$matrix-event:local",
		Body:          "hi from matrix",
	})
	if err != nil {
		t.Fatalf("HandleMatrixMessage returned error: %v", err)
	}
	if len(api.sent) != 1 {
		t.Fatalf("expected one Beeper send, got %d", len(api.sent))
	}
	if api.sent[0].ClientTxnID == "" {
		t.Fatal("expected deterministic client transaction ID")
	}
}

func TestMatrixToBeeperRemapsReplyTargetToBeeperMessageID(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-parent",
		MatrixEventID:   "$matrix-parent:local",
		ChatID:          "!chat:beeper",
		Version:         "parent",
	}); err != nil {
		t.Fatal(err)
	}
	api := &fakeBeeperAPI{}
	svc := NewService(DefaultConfig(), store, api, &fakeMatrixSink{})

	err := svc.HandleMatrixMessage(ctx, MatrixInbound{
		ChatID:        "!chat:beeper",
		MatrixEventID: "$matrix-reply:local",
		ReplyToEvent:  "$matrix-parent:local",
		Body:          "reply from matrix",
	})
	if err != nil {
		t.Fatalf("HandleMatrixMessage returned error: %v", err)
	}
	if len(api.sent) != 1 {
		t.Fatalf("expected one Beeper send, got %d", len(api.sent))
	}
	if api.sent[0].ReplyToID != "$beeper-parent" {
		t.Fatalf("expected Beeper reply ID to be remapped, got %q", api.sent[0].ReplyToID)
	}
}

func TestReconcileSuppressesEchoAfterMatrixToBeeperSend(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "signal", Name: "Echo"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$beeper-echo",
				ChatID:    "!chat:beeper",
				SenderID:  "@self:signal",
				Type:      MessageTypeText,
				Text:      "echo me once",
				Timestamp: time.Now().UTC(),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	if err := svc.HandleMatrixMessage(ctx, MatrixInbound{
		ChatID:        "!chat:beeper",
		MatrixEventID: "$matrix-origin:local",
		Body:          "echo me once",
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.events) != 0 {
		t.Fatalf("expected echoed Beeper message to be suppressed, got %#v", matrix.events)
	}
	mapping, ok, err := store.MessageByBeeperID(ctx, "$beeper-echo")
	if err != nil || !ok {
		t.Fatalf("expected echo mapping, ok=%v err=%v", ok, err)
	}
	if mapping.MatrixEventID != "$matrix-origin:local" {
		t.Fatalf("expected echo to map to original event, got %q", mapping.MatrixEventID)
	}
}

func TestReconcilePreservesMatrixOriginMappingWhenEchoVersionChanges(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "signal", Name: "Edited Echo"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:              "$beeper-echo",
				ChatID:          "!chat:beeper",
				SenderID:        "@self:signal",
				Type:            MessageTypeText,
				Text:            "edited in Matrix",
				Timestamp:       time.Now().UTC(),
				EditedTimestamp: ptrTime(time.Unix(200, 0).UTC()),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-echo",
		MatrixEventID:   "$matrix-origin:local",
		ChatID:          "!chat:beeper",
		Version:         "old",
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.events) != 0 {
		t.Fatalf("expected changed echo to preserve mapping without a duplicate Matrix event, got %#v", matrix.events)
	}
	mapping, ok, err := store.MessageByBeeperID(ctx, "$beeper-echo")
	if err != nil || !ok {
		t.Fatalf("expected echo mapping, ok=%v err=%v", ok, err)
	}
	if mapping.MatrixEventID != "$matrix-origin:local" {
		t.Fatalf("expected origin mapping to survive version update, got %q", mapping.MatrixEventID)
	}
	if mapping.Version == "old" {
		t.Fatal("expected version to be refreshed")
	}
}

func TestReconcileMirrorsBeeperEditAsMatrixReplacement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	editTime := time.Unix(200, 0).UTC()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "signal", Name: "Edited"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:              "$beeper-edit",
				AccountID:       "signal",
				ChatID:          "!chat:beeper",
				SenderID:        "@alice:signal",
				SenderName:      "Alice",
				Type:            MessageTypeText,
				Text:            "after edit",
				Timestamp:       time.Unix(100, 0).UTC(),
				EditedTimestamp: &editTime,
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-edit",
		MatrixEventID:   "$matrix-original:local",
		ChatID:          "!chat:beeper",
		Version:         "old",
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.edits) != 1 {
		t.Fatalf("expected one Matrix edit, got %#v", matrix.edits)
	}
	if matrix.edits[0].targetEventID != "$matrix-original:local" || matrix.edits[0].outbound.Body != "after edit" {
		t.Fatalf("unexpected edit %#v", matrix.edits[0])
	}
	mapping, ok, err := store.MessageByBeeperID(ctx, "$beeper-edit")
	if err != nil || !ok {
		t.Fatalf("expected mapping, ok=%v err=%v", ok, err)
	}
	if mapping.MatrixEventID != "$matrix-original:local" {
		t.Fatalf("expected edit to keep original mapping, got %q", mapping.MatrixEventID)
	}
	if mapping.Version == "old" {
		t.Fatal("expected version update after edit")
	}
}

func TestReconcileMirrorsBeeperDeleteAsMatrixRedaction(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	editTime := time.Unix(200, 0).UTC()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Deleted"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:              "$beeper-delete",
				AccountID:       "whatsapp",
				ChatID:          "!chat:beeper",
				SenderID:        "@bob:whatsapp",
				SenderName:      "Bob",
				Type:            MessageTypeText,
				Text:            "before delete",
				Timestamp:       time.Unix(100, 0).UTC(),
				EditedTimestamp: &editTime,
				IsDeleted:       true,
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-delete",
		MatrixEventID:   "$matrix-delete:local",
		ChatID:          "!chat:beeper",
		Version:         editTime.Format("20060102T150405.000000000Z"),
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.redactions) != 1 {
		t.Fatalf("expected one Matrix redaction, got %#v", matrix.redactions)
	}
	if matrix.redactions[0].eventID != "$matrix-delete:local" {
		t.Fatalf("unexpected redaction %#v", matrix.redactions[0])
	}
	mapping, ok, err := store.MessageByBeeperID(ctx, "$beeper-delete")
	if err != nil || !ok {
		t.Fatalf("expected mapping, ok=%v err=%v", ok, err)
	}
	if mapping.DeletedAt == nil {
		t.Fatal("expected mapping to be marked deleted")
	}
	if mapping.Version != "deleted" {
		t.Fatalf("expected deletion version to win over edit timestamp, got %q", mapping.Version)
	}
}

func TestReconcileBeeperMediaEditKeepsMediaOnReplacement(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	editTime := time.Unix(200, 0).UTC()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "whatsapp", Name: "Media Edit"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:              "$beeper-media-edit",
				AccountID:       "whatsapp",
				ChatID:          "!chat:beeper",
				SenderID:        "@alice:whatsapp",
				SenderName:      "Alice",
				Type:            MessageTypeImage,
				Text:            "edited caption",
				Timestamp:       time.Unix(100, 0).UTC(),
				EditedTimestamp: &editTime,
				Attachments: []Attachment{{
					ID:        "photo-1",
					URL:       "localmxc://photo-1",
					FileName:  "photo.jpg",
					MimeType:  "image/jpeg",
					SizeBytes: 10,
				}},
			}},
		},
		assets: map[string]string{"localmxc://photo-1": "imagebytes"},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	if err := store.UpsertMessageMapping(ctx, MessageMapping{
		BeeperMessageID: "$beeper-media-edit",
		MatrixEventID:   "$matrix-media:local",
		ChatID:          "!chat:beeper",
		Version:         "old",
	}); err != nil {
		t.Fatal(err)
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.edits) != 1 {
		t.Fatalf("expected one Matrix edit, got %#v", matrix.edits)
	}
	if matrix.edits[0].outbound.Media == nil {
		t.Fatal("expected media edit replacement to preserve/reupload media")
	}
	if matrix.edits[0].outbound.Media.FileName != "photo.jpg" {
		t.Fatalf("unexpected media %#v", matrix.edits[0].outbound.Media)
	}
}

func TestReconcileBeeperDeleteRedactsStoredAttachmentsWhenTombstoneOmitsThem(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	api := &fakeBeeperAPI{
		chats: []Chat{{ID: "!chat:beeper", AccountID: "signal", Name: "Deleted album"}},
		messages: map[string][]Message{
			"!chat:beeper": {{
				ID:        "$beeper-album",
				AccountID: "signal",
				ChatID:    "!chat:beeper",
				SenderID:  "@alice:signal",
				Type:      MessageTypeText,
				IsDeleted: true,
				Timestamp: time.Unix(100, 0).UTC(),
			}},
		},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)
	for _, mapping := range []MessageMapping{
		{BeeperMessageID: "$beeper-album", MatrixEventID: "$matrix-primary:local", ChatID: "!chat:beeper", Version: "old"},
		{BeeperMessageID: "$beeper-album/attachment/1", MatrixEventID: "$matrix-attachment-1:local", ChatID: "!chat:beeper", Version: "old"},
		{BeeperMessageID: "$beeper-album/attachment/2", MatrixEventID: "$matrix-attachment-2:local", ChatID: "!chat:beeper", Version: "old"},
	} {
		if err := store.UpsertMessageMapping(ctx, mapping); err != nil {
			t.Fatal(err)
		}
	}

	if err := svc.ReconcileOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.redactions) != 3 {
		t.Fatalf("expected primary and two attachment redactions, got %#v", matrix.redactions)
	}
	attachment, ok, err := store.MessageByBeeperID(ctx, "$beeper-album/attachment/1")
	if err != nil || !ok {
		t.Fatalf("expected attachment mapping, ok=%v err=%v", ok, err)
	}
	if attachment.DeletedAt == nil {
		t.Fatal("expected attachment mapping to be marked deleted")
	}
}

func TestReconcileAvatarFallbackDoesNotMarkFailedRealAvatarSynced(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	chat := Chat{
		ID:        "!chat:beeper",
		AccountID: "telegram",
		Network:   "Telegram",
		Name:      "Avatar retry",
		AvatarURL: "localmxc://real-avatar",
	}
	api := &fakeBeeperAPI{
		chats:       []Chat{chat},
		messages:    map[string][]Message{"!chat:beeper": nil},
		assets:      map[string]string{"localmxc://real-avatar": "real-avatar"},
		assetErrors: map[string]error{"localmxc://real-avatar": errors.New("missing once")},
	}
	matrix := &fakeMatrixSink{}
	svc := NewService(DefaultConfig(), store, api, matrix)

	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatal(err)
	}
	syncValue, err := store.GetValue(ctx, portalAvatarSyncKey(chat.ID))
	if err != nil {
		t.Fatal(err)
	}
	if syncValue != platformAvatarSyncValue(chat) {
		t.Fatalf("expected failed real avatar to store platform sync value, got %q", syncValue)
	}

	delete(api.assetErrors, "localmxc://real-avatar")
	if err := svc.ReconcilePortalsOnly(ctx); err != nil {
		t.Fatal(err)
	}
	if len(matrix.avatars) != 2 {
		t.Fatalf("expected second run to retry and upload real avatar, got %d avatars", len(matrix.avatars))
	}
	syncValue, err = store.GetValue(ctx, portalAvatarSyncKey(chat.ID))
	if err != nil {
		t.Fatal(err)
	}
	if syncValue != "localmxc://real-avatar" {
		t.Fatalf("expected real avatar sync value after retry, got %q", syncValue)
	}
}

func ptrTime(t time.Time) *time.Time { return &t }

type fakeBeeperAPI struct {
	chats            []Chat
	messages         map[string][]Message
	messagePages     map[string][]MessagePage
	assets           map[string]string
	assetErrors      map[string]error
	downloadedAssets []string
	sent             []BeeperOutbound
	updates          []BeeperOutbound
	deletes          []string
	reactions        []string
}

func (f *fakeBeeperAPI) Health(context.Context) error { return nil }

func (f *fakeBeeperAPI) ListChats(context.Context) ([]Chat, error) {
	return f.chats, nil
}

func (f *fakeBeeperAPI) ListMessages(ctx context.Context, chatID string, afterCursor string, limit int) ([]Message, string, error) {
	return f.messages[chatID], "cursor-" + chatID, nil
}

func (f *fakeBeeperAPI) ListMessagePage(ctx context.Context, chatID string, cursor string, direction string) (MessagePage, error) {
	pages := f.messagePages[chatID]
	if len(pages) == 0 {
		return MessagePage{}, nil
	}
	page := pages[0]
	f.messagePages[chatID] = pages[1:]
	return page, nil
}

func (f *fakeBeeperAPI) DownloadAsset(ctx context.Context, assetURL string) (*AssetStream, error) {
	f.downloadedAssets = append(f.downloadedAssets, assetURL)
	if err := f.assetErrors[assetURL]; err != nil {
		return nil, err
	}
	return &AssetStream{
		Content:   io.NopCloser(strings.NewReader(f.assets[assetURL])),
		MimeType:  "application/octet-stream",
		SizeBytes: int64(len(f.assets[assetURL])),
	}, nil
}

func (f *fakeBeeperAPI) SendMessage(ctx context.Context, outbound BeeperOutbound) (string, error) {
	f.sent = append(f.sent, outbound)
	return "$beeper-sent", nil
}

func (f *fakeBeeperAPI) UpdateMessage(ctx context.Context, chatID, messageID, text string) error {
	f.updates = append(f.updates, BeeperOutbound{ChatID: chatID, Text: text, ClientTxnID: messageID})
	return nil
}

func (f *fakeBeeperAPI) DeleteMessage(ctx context.Context, chatID, messageID string, forEveryone bool) error {
	f.deletes = append(f.deletes, messageID)
	return nil
}

func (f *fakeBeeperAPI) AddReaction(ctx context.Context, chatID, messageID, reactionKey, txnID string) error {
	f.reactions = append(f.reactions, chatID+"|"+messageID+"|"+reactionKey+"|"+txnID)
	return nil
}

func (f *fakeBeeperAPI) RemoveReaction(ctx context.Context, chatID, messageID, reactionKey string) error {
	f.reactions = append(f.reactions, "delete|"+chatID+"|"+messageID+"|"+reactionKey)
	return nil
}

type fakeMatrixSink struct {
	mu                  sync.Mutex
	events              []MatrixOutbound
	edits               []fakeMatrixEdit
	redactions          []fakeMatrixRedaction
	failMedia           bool
	failPortals         map[string]error
	failPortalSequences map[string][]error
	inaccessibleRooms   map[string]bool
	accessErrs          map[string]error
	portalAttempts      map[string]int
	avatars             []*MatrixMedia
	spaceChats          []Chat
}

type fakeMatrixEdit struct {
	outbound      MatrixOutbound
	targetEventID string
}

type fakeMatrixRedaction struct {
	roomID  string
	eventID string
	txnID   string
	reason  string
}

func (f *fakeMatrixSink) EnsurePortal(ctx context.Context, chat Chat, avatar *MatrixMedia) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.portalAttempts == nil {
		f.portalAttempts = make(map[string]int)
	}
	attempt := f.portalAttempts[chat.ID]
	f.portalAttempts[chat.ID] = attempt + 1
	if seq := f.failPortalSequences[chat.ID]; attempt < len(seq) && seq[attempt] != nil {
		return "", seq[attempt]
	}
	if err := f.failPortals[chat.ID]; err != nil {
		return "", err
	}
	if avatar != nil {
		f.avatars = append(f.avatars, avatar)
	}
	return "!matrix-" + chat.ID, nil
}

func (f *fakeMatrixSink) PortalAccessible(ctx context.Context, roomID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.accessErrs[roomID]; err != nil {
		return false, err
	}
	return !f.inaccessibleRooms[roomID], nil
}

func (f *fakeMatrixSink) EnsurePortalSpaces(ctx context.Context, chats []Chat) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.spaceChats = append([]Chat(nil), chats...)
	return nil
}

func (f *fakeMatrixSink) EnsurePuppet(ctx context.Context, sender Sender) (string, error) {
	return "@" + MatrixGhostLocalpart(sender.ID) + ":local", nil
}

func (f *fakeMatrixSink) SendMessage(ctx context.Context, outbound MatrixOutbound) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events = append(f.events, outbound)
	if f.failMedia && outbound.Media != nil {
		return "", errors.New("HTTP 413")
	}
	return "$event-" + outbound.MessageID + ":local", nil
}

func (f *fakeMatrixSink) EditMessage(ctx context.Context, outbound MatrixOutbound, targetEventID string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.edits = append(f.edits, fakeMatrixEdit{outbound: outbound, targetEventID: targetEventID})
	return "$edit-" + outbound.MessageID + ":local", nil
}

func (f *fakeMatrixSink) RedactMessage(ctx context.Context, roomID string, eventID string, txnID string, reason string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.redactions = append(f.redactions, fakeMatrixRedaction{roomID: roomID, eventID: eventID, txnID: txnID, reason: reason})
	return "$redact-" + eventID + ":local", nil
}
