package prism

import (
	"context"
	"fmt"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// Helpers

func mockPostmaster(
	ctx context.Context,
) (pm *Postmaster, poolEvents chan PoolEvent) {
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents = make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }
	pm = NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)
	return
}

func expectLetterOutcome(
	t *testing.T, ch chan LetterOutcome, kind LetterOutcomeKind,
) {
	t.Helper()

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-ch:
			return true
		}
	}, "should have received outcome")

	assert.Equal(t, kind, outcome.Kind)
}

func expectAllLetterOutcomes(
	t *testing.T, ch chan LetterOutcome, kind LetterOutcomeKind, count int,
) {
	t.Helper()

	outcomes := make([]LetterOutcome, 0, count)
	Eventually(t, func() bool {
		select {
		default:
			return false
		case o := <-ch:
			outcomes = append(outcomes, o)
			return len(outcomes) == count
		}
	}, fmt.Sprintf("should have returned %d outcomes", count))

	if len(outcomes) >= count {
		for i := range count {
			assert.Equal(t, OutcomeCancelled, outcomes[i].Kind)
		}
	}
}

// Tests

func TestPostmasterUnknownPeerSend(t *testing.T) {
	ctx := context.Background()
	pm, _ := mockPostmaster(ctx)

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

	expectLetterOutcome(t, called, OutcomeRejected)
}

func TestPostmasterSend(t *testing.T) {
	ctx := context.Background()
	pm, poolEvents := mockPostmaster(ctx)

	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	poolEvents <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}

	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

	expectLetterOutcome(t, called, OutcomeSent)
}

func TestPostmasterCancelInFlight(t *testing.T) {
	ctx := context.Background()
	pm, poolEvents := mockPostmaster(ctx)

	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	_, cancel := pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

	// wait for letter to queue
	time.Sleep(100 * time.Millisecond)

	// cancel the letter using its callback
	cancel()

	// connect the pool
	poolEvents <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}

	expectLetterOutcome(t, called, OutcomeCancelled)
}

func TestPostmasterExpire(t *testing.T) {
	ctx := context.Background()
	pm, poolEvents := mockPostmaster(ctx)

	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o },
		WithDeadline(1*time.Millisecond))

	// wait for letter to queue and expire
	time.Sleep(100 * time.Millisecond)

	// connect the pool
	poolEvents <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}

	expectLetterOutcome(t, called, OutcomeExpired)
}

func TestPostmasterPeerRemoved(t *testing.T) {
	ctx := context.Background()
	pm, poolEvents := mockPostmaster(ctx)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// send two letters
	called := make(chan LetterOutcome, 2)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

	// wait for them to hit the courier queue
	time.Sleep(100 * time.Millisecond)

	// remove the peer
	poolEvents <- PoolEvent{ID: "peer", Kind: EventRemoved, At: time.Now()}

	// expect each letter to return cancelled
	expectAllLetterOutcomes(t, called, OutcomeCancelled, 2)

	// subsequent sends should fail
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })
	expectLetterOutcome(t, called, OutcomeRejected)
}

func TestPostmasterCourierCloseRace(t *testing.T) {
	ctx := context.Background()
	pm, poolEvents := mockPostmaster(ctx)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// remove the peer
	poolEvents <- PoolEvent{ID: "peer", Kind: EventRemoved, At: time.Now()}

	// send a letter
	time.Sleep(5 * time.Microsecond) // small wait lines up the race condition
	var outcome *LetterOutcome
	called := make(chan LetterOutcome, 1)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

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
	pm, poolEvents := mockPostmaster(ctx)

	// add peer, but do not connect
	poolEvents <- PoolEvent{ID: "peer", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 }, "should add peer")

	// send two letters
	called := make(chan LetterOutcome, 2)
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })

	// wait for them to hit the courier queue
	time.Sleep(100 * time.Millisecond)

	// close postmaster
	pm.Close()

	// expect each letter to return cancelled
	expectAllLetterOutcomes(t, called, OutcomeCancelled, 2)

	// subsequent sends should be rejected
	pm.Send(ctx, "peer", nil, func(o LetterOutcome) { called <- o })
	expectLetterOutcome(t, called, OutcomeRejected)
}
