package peerstat

import (
	"testing"
)

func TestNoopSink(t *testing.T) {
	// Test that NoopSink implements the Sink interface
	var _ Sink = NoopSink{}

	// Test that calling Record doesn't panic or crash
	sink := NoopSink{}
	sink.Record("peer1", "test event")
	sink.Record("", nil)

	// Test that it's a no-op (no assertions needed since it does nothing)
}
