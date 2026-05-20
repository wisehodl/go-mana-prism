// Package observer defines the contract for collecting peer-level statistics.
package observer

// Observer is the interface through which add-ons report statistics.
// Add-ons should call Record() with a peer ID and a typed event when
// significant events occur. The event parameter is expected to be a
// domain-specific struct defined in the add-on's package.
type Observer interface {
	// Record reports a statistic event for a given peer.
	// The event parameter contains the domain-specific details.
	Record(peerID string, event any)
}

// NullObserver is a null implementation of Observer that does nothing.
// It is used when no actual statistics collector is wired in.
type NullObserver struct{}

// Record implements Observer for NullObserver, doing nothing.
func (NullObserver) Record(_ string, _ any) {}
