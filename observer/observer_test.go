package observer

import (
	"testing"
)

func TestNullObserver(t *testing.T) {
	// Test that NullObserver implements the Observer interface
	var _ Observer = NullObserver{}

	// Test that calling Record doesn't panic or crash
	sink := NullObserver{}
	sink.Record("peer1", "test event")
	sink.Record("", nil)

	// Test that it's a no-op (no assertions needed since it does nothing)
}
