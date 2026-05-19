// Package peerstat defines the contract for collecting peer-level statistics.
package peerstat

// Sink is the interface through which add-ons report statistics.
// Add-ons should call Record() with a peer ID and a typed event when
// significant events occur. The event parameter is expected to be a
// domain-specific struct defined in the add-on's package.
type Sink interface {
	// Record reports a statistic event for a given peer.
	// The event parameter contains the domain-specific details.
	Record(peerID string, event any)
}

// NoopSink is a null implementation of Sink that does nothing.
// It is used when no actual statistics collector is wired in.
type NoopSink struct{}

// Record implements Sink for NoopSink, doing nothing.
func (NoopSink) Record(_ string, _ any) {}
