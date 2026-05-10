package prism

import (
	"context"
	// "git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
	// "sync/atomic"
	"testing"
	"time"
)

func TestPostmasterUnknownPeerSend(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	_, err := pm.Send(ctx, "wss://test", []byte("[]"), func(LetterOutcome) {})
	assert.Error(t, err)
}

func TestPostmasterSend(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventAdded, At: time.Now()}
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventConnected, At: time.Now()}

	Eventually(t, func() bool { return len(pm.Peers()) > 0 },
		"should add peer")

	called := make(chan LetterOutcome, 1)
	_, err := pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.NoError(t, err)

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

func TestPostmasterPeerRemoved(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 },
		"should add peer")

	// send two letters
	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 2)
	_, err := pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.NoError(t, err)
	_, err = pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.NoError(t, err)

	// wait for them to hit the courier queue
	time.Sleep(100 * time.Millisecond)

	// remove the peer
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventRemoved, At: time.Now()}

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
	_, err = pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.Error(t, err)
}

func TestPostmasterCourierCloseRace(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 },
		"should add peer")

	// remove the peer
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventRemoved, At: time.Now()}

	// send a letter
	time.Sleep(5 * time.Microsecond) // small wait lines up the race condition
	var outcome LetterOutcome
	called := make(chan LetterOutcome, 1)
	_, err := pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })

	if err != nil {
		// the close won the race, the letter was not sent
		return
	}

	// the letter might beat the courier close and return cancelled
	Eventually(t, func() bool {
		select {
		default:
			return false
		case outcome = <-called:
			return true
		}
	}, "should have returned 1 outcomes")

	if outcome.LetterID == 0 {
		t.Fatal("did not receive an outcome")
	}

	assert.Equal(t, OutcomeCancelled, outcome.Kind)
}

func TestPostmasterClose(t *testing.T) {
	ctx := context.Background()
	poolHasPeer := func(id string) (string, bool) { return id, true }
	poolEvents := make(chan PoolEvent, 4)
	poolSendFunc := func(id string, data Envelope) error { return nil }

	pm := NewPostmaster(ctx, poolHasPeer, poolEvents, poolSendFunc, nil)

	// add peer, but do not connect
	poolEvents <- PoolEvent{
		ID: "wss://test", Kind: EventAdded, At: time.Now()}
	Eventually(t, func() bool { return len(pm.Peers()) > 0 },
		"should add peer")

	// send two letters
	outcomes := make([]LetterOutcome, 0, 2)
	called := make(chan LetterOutcome, 2)
	_, err := pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.NoError(t, err)
	_, err = pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.NoError(t, err)

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

	// subsequent sends should fail
	_, err = pm.Send(
		ctx, "wss://test", []byte("[]"), func(o LetterOutcome) { called <- o })
	assert.Error(t, err)
}
