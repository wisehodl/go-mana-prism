package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
	"testing"
	// "time"
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

	sent := make(chan []byte, 1)
	sendFunc := func(data Envelope) error {
		sent <- data
		return nil
	}

	c := NewCourier(ctx, sendFunc, nil)
	called := make(chan LetterOutcome, 1)
	c.Enqueue(newTestLetter(ctx, 1), func(o LetterOutcome) { called <- o })

	Never(t, func() bool { return len(sent) > 0 },
		"should not have sent while disconnected")

	c.HandleConnect()

	Eventually(t, func() bool { return len(sent) > 0 },
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
	assert.Equal(t, "sent", outcome.Kind.String())
	assert.False(t, outcome.SentAt.IsZero())
	assert.True(t, outcome.MissedAt.IsZero())
	assert.Equal(t, 0, outcome.Retries)
}

func TestCourierSequentialSends(t *testing.T) {

}

func TestCourierSkipsCancelledLetter(t *testing.T) {

}

func TestCourierRetryOnFailure(t *testing.T) {

}

func TestCourierPauseOnDisconnect(t *testing.T) {

}

func TestCourierDrainOnClose(t *testing.T) {

}
