package beepersource

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type retryAttachmentRedactionSink struct {
	fakeMatrixSink
	attempts map[string]int
}

func (f *retryAttachmentRedactionSink) RedactMessage(ctx context.Context, roomID, eventID, txnID, reason string) (string, error) {
	f.attempts[eventID]++
	if eventID == "$secondary-2" && f.attempts[eventID] == 1 {
		return "", errors.New("temporary secondary redaction failure")
	}
	return f.fakeMatrixSink.RedactMessage(ctx, roomID, eventID, txnID, reason)
}

func TestDeletionRetryCleansRemainingAttachmentsWithoutAttachmentDTO(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	defer store.Close()
	sink := &retryAttachmentRedactionSink{attempts: map[string]int{}}
	svc := NewService(DefaultConfig(), store, &fakeBeeperAPI{}, sink)
	msg := Message{ID: "$album", ChatID: "!chat:local", Type: MessageTypeText, IsDeleted: true}
	for index, eventID := range []string{"$primary", "$secondary-1", "$secondary-2"} {
		messageID := msg.ID
		if index > 0 {
			messageID = fmt.Sprintf("%s/attachment/%d", msg.ID, index)
		}
		if err := store.UpsertMessageMapping(ctx, MessageMapping{BeeperMessageID: messageID, MatrixEventID: eventID, ChatID: msg.ChatID, Version: "before-delete"}); err != nil {
			t.Fatal(err)
		}
	}
	if err := svc.mirrorMessage(ctx, "!room:local", Chat{ID: msg.ChatID}, msg); err == nil {
		t.Fatal("expected the transient secondary-redaction error")
	}
	if err := svc.mirrorMessage(ctx, "!room:local", Chat{ID: msg.ChatID}, msg); err != nil {
		t.Fatal(err)
	}
	for _, messageID := range []string{msg.ID, msg.ID + "/attachment/1", msg.ID + "/attachment/2"} {
		mapping, ok, err := store.MessageByBeeperID(ctx, messageID)
		if err != nil || !ok || mapping.DeletedAt == nil {
			t.Fatalf("deletion retry left mapping visible: %s ok=%v err=%v", messageID, ok, err)
		}
	}
	if sink.attempts["$primary"] != 1 || sink.attempts["$secondary-1"] != 1 || sink.attempts["$secondary-2"] != 2 {
		t.Fatalf("expected only unfinished redaction to retry: %#v", sink.attempts)
	}
	if len(sink.events) != 0 || len(sink.edits) != 0 {
		t.Fatal("a deletion retry must not recreate media")
	}
}

func TestConcurrentDirectMappingsPreserveEveryPortal(t *testing.T) {
	var mu sync.Mutex
	direct := map[string][]string{"@existing:local": {"!existing:local"}}
	firstGet := make(chan struct{})
	releaseFirst := make(chan struct{})
	otherPut := make(chan struct{}, 1)
	getCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			mu.Lock()
			getCount++
			first := getCount == 1
			body, _ := json.Marshal(direct)
			mu.Unlock()
			if first {
				close(firstGet)
				<-releaseFirst
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write(body)
		case http.MethodPut:
			var body map[string][]string
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid fixture request", http.StatusBadRequest)
				return
			}
			mu.Lock()
			direct = body
			mu.Unlock()
			select {
			case otherPut <- struct{}{}:
			default:
			}
			_, _ = w.Write([]byte("{}"))
		default:
			http.Error(w, "unexpected fixture method", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	cfg := DefaultConfig()
	cfg.Matrix.HomeserverURL = server.URL
	cfg.Matrix.UserID = "@proxy:local"
	sink, err := NewMatrixClientSink(cfg, nil, "synthetic-test-token")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan error, 8)
	update := func(index int) {
		chat := Chat{ID: fmt.Sprintf("chat-%d", index), Participants: []Sender{{ID: fmt.Sprintf("@person-%d:local", index)}}}
		results <- sink.syncDirectMapping(context.Background(), chat, fmt.Sprintf("!room-%d:local", index))
	}
	go update(0)
	<-firstGet
	for index := 1; index < 8; index++ {
		go update(index)
	}
	// An unprotected second roundtrip writes a stale snapshot while the first
	// GET is held. With serialization, release the first roundtrip normally.
	select {
	case <-otherPut:
	case <-time.After(200 * time.Millisecond):
	}
	close(releaseFirst)
	for index := 0; index < 8; index++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(direct) != 9 {
		t.Fatalf("parallel updates lost DM mappings: got %d, want 9", len(direct))
	}
	for index := 0; index < 8; index++ {
		target := fmt.Sprintf("@person-%d:local", index)
		if !stringSliceContains(direct[target], fmt.Sprintf("!room-%d:local", index)) {
			t.Errorf("missing mapping for %s", target)
		}
	}
}
