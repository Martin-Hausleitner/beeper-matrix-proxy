package beepersource

import (
	"testing"
	"time"
)

func TestDeterministicTxnIDIsStableAndScopedByMutationVersion(t *testing.T) {
	first := DeterministicTxnID("!chat:beeper", "$msg:beeper", MutationMessage, "v1")
	again := DeterministicTxnID("!chat:beeper", "$msg:beeper", MutationMessage, "v1")
	edit := DeterministicTxnID("!chat:beeper", "$msg:beeper", MutationEdit, "v2")

	if first != again {
		t.Fatalf("expected stable txn ID, got %q and %q", first, again)
	}
	if first == edit {
		t.Fatal("expected edit/version mutation to produce a different txn ID")
	}
	if len(first) > 64 {
		t.Fatalf("txn ID is too long for practical Matrix use: %d", len(first))
	}
}

func TestMatrixGhostIDIsStableAndSanitized(t *testing.T) {
	got := MatrixGhostLocalpart("@alice:local-whatsapp.localhost")
	again := MatrixGhostLocalpart("@alice:local-whatsapp.localhost")
	other := MatrixGhostLocalpart("@alice:local-signal.localhost")

	if got != again {
		t.Fatalf("expected stable ghost ID, got %q and %q", got, again)
	}
	if got == other {
		t.Fatal("expected source network to affect ghost ID")
	}
	if len(got) < len("beeper_") || got[:len("beeper_")] != "beeper_" {
		t.Fatalf("unexpected ghost localpart %q", got)
	}
}

func TestMessageVersionPrioritizesDeletionOverEditTimestamp(t *testing.T) {
	edited := time.Unix(200, 0).UTC()
	version := MessageVersion(Message{
		Timestamp:       time.Unix(100, 0).UTC(),
		EditedTimestamp: &edited,
		IsDeleted:       true,
	})
	if version != "deleted" {
		t.Fatalf("expected deleted version to win, got %q", version)
	}
}
