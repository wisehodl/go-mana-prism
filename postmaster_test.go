package prism

import (
	"context"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

const testURL = "wss://test"

func TestPostmasterUnknownPeerSend(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	assert.Equal(t, OutcomeRejected, outcome.Kind)
}

func TestPostmasterSend(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	poolEvents <- PoolEvent{ID: testURL, Kind: EventConnected, At: time.Now()}

	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	assert.Equal(t, OutcomeSent, outcome.Kind)
}

func TestPostmasterCancelInFlight(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	cancel := pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	// wait for letter to queue
	time.Sleep(100 * time.Millisecond)

	// cancel the letter using its callback
	cancel()

	// connect the pool
	poolEvents <- PoolEvent{ID: testURL, Kind: EventConnected, At: time.Now()}

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	// letter should drain out of the queue and return cancelled
	assert.Equal(t, OutcomeCancelled, outcome.Kind)
}

func TestPostmasterExpire(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o },
		WithDeadline(1*time.Millisecond))

	// wait for letter to queue and expire
	time.Sleep(100 * time.Millisecond)

	// connect the pool
	poolEvents <- PoolEvent{ID: testURL, Kind: EventConnected, At: time.Now()}

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	// letter should drain out of the queue and return expired
	assert.Equal(t, OutcomeExpired, outcome.Kind)
}

func TestPostmasterPeerRemoved(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// send two letters
	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 2)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	// wait for them to hit the courier queue
	time.Sleep(100 * time.Millisecond)

	// remove the peer
	poolEvents <- PoolEvent{ID: testURL, Kind: EventRemoved, At: time.Now()}

	// expect each letter to return cancelled
	Eventually(t, func() bool {
		select {
		default:
			return false
		case o := <-called:
			outcomes = append(outcomes, o)
			return len(outcomes) == 2
		}
	}, "should have returned 2 outcomes")

	if len(outcomes) >= 2 {
		assert.Equal(t, OutcomeCancelled, outcomes[0].Kind)
		assert.Equal(t, OutcomeCancelled, outcomes[1].Kind)
	}

	// subsequent sends should fail
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	assert.Equal(t, OutcomeRejected, outcome.Kind)
}

func TestPostmasterCourierCloseRace(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// remove the peer
	poolEvents <- PoolEvent{ID: testURL, Kind: EventRemoved, At: time.Now()}

	// send a letter
	time.Sleep(5 * time.Microsecond) // small wait lines up the race condition
	var outcome *LetterOutcome
	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	Eventually(t, func() bool {
		select {
		default:
			return false
		case o := <-called:
			outcome = &o
			return true
		}
	}, "should have returned 1 outcomes")

	if outcome == nil {
		t.Fatal("did not receive an outcome")
	}

	// depending on the race, the outcome could be:
	// close, then send: send is rejected by the postmaster
	// send, then close: send is cancelled by the courier
	assert.Contains(t,
		[]LetterOutcomeKind{OutcomeCancelled, OutcomeRejected},
		outcome.Kind,
	)
}

func TestPostmasterClose(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: testURL, Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// send two letters
	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 2)
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	// wait for them to hit the courier queue
	time.Sleep(100 * time.Millisecond)

	// close postmaster
	pm.Close()

	// expect each letter to return cancelled
	Eventually(t, func() bool {
		select {
		default:
			return false
		case o := <-called:
			outcomes = append(outcomes, o)
			return len(outcomes) == 2
		}
	}, "should have returned 2 outcomes")

	if len(outcomes) >= 2 {
		assert.Equal(t, OutcomeCancelled, outcomes[0].Kind)
		assert.Equal(t, OutcomeCancelled, outcomes[1].Kind)
	}

	// subsequent sends should be rejected
	pm.Send(ctx, testURL, nil, func(o LetterOutcome) { called <- o })

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have received outcome")

	assert.Equal(t, OutcomeRejected, outcome.Kind)
}
