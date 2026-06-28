package handlers

import "testing"

// triggerTranslationSync is called from the request path on approval, so it
// must never block and must coalesce a burst into a single queued sync.
func TestTriggerTranslationSyncCoalesces(t *testing.T) {
	// Start from a known-empty state.
	select {
	case <-translationSyncCh:
	default:
	}

	triggerTranslationSync()
	triggerTranslationSync()
	triggerTranslationSync()

	if got := len(translationSyncCh); got != 1 {
		t.Fatalf("queued signals = %d, want 1 (coalesced, non-blocking)", got)
	}
	select {
	case <-translationSyncCh:
	default:
		t.Fatal("expected exactly one queued signal")
	}
}
