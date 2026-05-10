package prism

import (
	"context"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
	"sync/atomic"
	"testing"
	"time"
)

// Helpers

func newTestLetter(ctx context.Context, id uint64) OutboundLetter {
	ctx, cancel := context.WithCancel(
		component.MustExtend(ctx, "test_letter"))
	return OutboundLetter{
		id:     id,
		peerID: "wss://test",
		data:   []byte("[]"),
		ctx:    ctx,
		cancel: cancel,
	}
}

// Tests

func TestCourierSendsAfterConnect(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	sendFunc := func(data Envelope) error {
		sendCount.Add(1)
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)
	called := make(chan LetterOutcome, 1)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })

	Never(t, func() bool { return sendCount.Load() > 0 },
		"should not have sent while disconnected")

	c.HandleConnect()

	Eventually(t, func() bool { return sendCount.Load() > 0 },
		"should have sent after connect")

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have returned outcome")

	assert.Equal(t, uint64(1), outcome.LetterID)
	assert.Equal(t, "wss://test", outcome.PeerID)
	assert.Equal(t, OutcomeSent, outcome.Kind)
	assert.False(t, outcome.SentAt.IsZero())
	assert.True(t, outcome.MissedAt.IsZero())
	assert.Equal(t, 0, outcome.Retries)
}

func TestCourierMultipleSends(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	sendFunc := func(data Envelope) error {
		sendCount.Add(1)
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)
	c.HandleConnect()

	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 4)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })
	c.Enqueue(newTestLetter(ctx, 2), func(o LetterOutcome) { called <- o })

	Eventually(t, func() bool { return sendCount.Load() == 2 },
		"should have sent letters")

	Eventually(t, func() bool {
		select {
		default:
			return false
		case o := <-called:
			outcomes = append(outcomes, o)
			return len(outcomes) == 2
		}
	}, "should have returned 2 outcomes")

	// callbacks are called in goroutines and may arrive out of order
	assert.Equal(t, OutcomeSent, outcomes[0].Kind)
	assert.Equal(t, OutcomeSent, outcomes[1].Kind)
}

func TestCourierSkipsCancelledLetter(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	sendFunc := func(data Envelope) error {
		sendCount.Add(1)
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)
	c.HandleConnect()

	l := newTestLetter(ctx, 1)
	l.cancel()

	called := make(chan LetterOutcome, 1)
	c.Enqueue(l, func(o LetterOutcome) { called <- o })

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have returned outcome")

	assert.Equal(t, OutcomeCancelled, outcome.Kind)
}

func TestCourierRetryOnFailure(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	sendFunc := func(data Envelope) error {
		sendCount.Add(1)
		if sendCount.Load() < 3 {
			return fmt.Errorf("transient failure")
		}
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)
	c.HandleConnect()

	called := make(chan LetterOutcome, 1)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })

	Eventually(t, func() bool { return sendCount.Load() > 0 },
		"should send eventually")

	var outcome LetterOutcome
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have returned outcome")

	assert.Equal(t, OutcomeSent, outcome.Kind)
	assert.Equal(t, 2, outcome.Retries)
}

func TestCourierPauseOnDisconnect(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	var gate atomic.Bool
	gate.Store(false)
	sendFunc := func(data Envelope) error {
		// gated send
		if gate.Load() {
			sendCount.Add(1)
			return nil
		}

		return fmt.Errorf("gate is closed")
	}

	c := NewCourier(ctx, sendFunc, nil)
	c.HandleConnect()

	// queue a letter
	called := make(chan LetterOutcome, 1)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })

	// manually wait for letters to queue
	time.Sleep(100 * time.Millisecond)

	// manually wait for disconnect toggle
	c.HandleDisconnect()
	time.Sleep(100 * time.Millisecond)

	// open gate
	gate.Store(true)

	// should never have sent in this time
	Never(t, func() bool { return sendCount.Load() > 0 },
		"should not have sent while disconnected")

	// reconnect, gate is open, letter should send
	c.HandleConnect()
	Eventually(t, func() bool { return sendCount.Load() > 0 },
		"should have sent")
}

func TestCourierDrainOnClose(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")

	var sendCount atomic.Uint32
	sendFunc := func(data Envelope) error {
		sendCount.Add(1)
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)

	// do not connect, queue some letters
	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 4)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })
	c.Enqueue(newTestLetter(ctx, 2), func(o LetterOutcome) { called <- o })

	// should not send any letters
	Never(t, func() bool { return sendCount.Load() > 0 },
		"should not have sent letters")

	// close the courier
	c.Close()

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
}
