package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

func mockInbox() (chan honeybee.InboxMessage, func(label string)) {
	ch := make(chan honeybee.InboxMessage, 8)
	inject := func(label string) {
		ch <- honeybee.InboxMessage{
			ID:         "wss://test",
			Data:       []byte(`["` + label + `","payload"]`),
			ReceivedAt: time.Now(),
		}
	}
	return ch, inject
}

func makeClerk(inbox chan honeybee.InboxMessage) *Clerk {
	known := map[string]struct{}{
		"EVENT": {},
		"EOSE":  {},
		"CLOSE": {},
	}
	return NewClerk(context.Background(), inbox, known, nil)
}

// ----------------------------------------------------------------------------
// Tests
// ----------------------------------------------------------------------------

func TestClerkRouting(t *testing.T) {
	inbox, inject := mockInbox()
	c := makeClerk(inbox)

	subA, err := c.Subscribe(map[string]struct{}{"EVENT": {}}, 4)
	assert.NoError(t, err)

	subB, err := c.Subscribe(map[string]struct{}{"EVENT": {}, "EOSE": {}}, 4)
	assert.NoError(t, err)

	assert.NoError(t, c.Start())

	inject("EVENT")
	inject("EOSE")

	// A receives exactly one letter (EVENT only)
	Eventually(t, func() bool {
		select {
		case l := <-subA:
			return string(l.Data) == `["EVENT","payload"]`
		default:
			return false
		}
	}, "subA should receive the EVENT letter")

	Never(t, func() bool {
		select {
		case <-subA:
			return true
		default:
			return false
		}
	}, "subA should receive no further letters")

	// B receives two letters (EVENT and EOSE)
	count := 0
	Eventually(t, func() bool {
		select {
		case <-subB:
			count++
		default:
		}
		return count == 2
	}, "subB should receive both letters")
}

func TestClerkStartup(t *testing.T) {
	inbox, _ := mockInbox()
	c := makeClerk(inbox)

	assert.NoError(t, c.Start())

	_, err := c.Subscribe(map[string]struct{}{"EVENT": {}}, 4)
	assert.ErrorIs(t, err, ErrAlreadyStarted)

	c.Close()
}

func TestClerkUnknownSubscriptionLabel(t *testing.T) {
	inbox, _ := mockInbox()
	c := makeClerk(inbox)

	_, err := c.Subscribe(map[string]struct{}{"UNKNOWN": {}}, 4)
	assert.ErrorIs(t, err, ErrUnknownLabel)
}

func TestClerkUnknownInboxLabel(t *testing.T) {
	inbox, inject := mockInbox()
	c := makeClerk(inbox)

	// subscribe to every known label
	sub, err := c.Subscribe(
		map[string]struct{}{"EVENT": {}, "EOSE": {}, "CLOSE": {}}, 4)
	assert.NoError(t, err)
	assert.NoError(t, c.Start())

	// inject a valid nostr label, but is not in the test label set
	inject("NOTICE")

	Never(t, func() bool {
		select {
		case <-sub:
			return true
		default:
			return false
		}
	}, "no subscriber should receive an unknown label")
}

func TestClerkInboxClose(t *testing.T) {
	inbox, _ := mockInbox()
	c := makeClerk(inbox)

	sub, err := c.Subscribe(map[string]struct{}{"EVENT": {}}, 4)
	assert.NoError(t, err)
	assert.NoError(t, c.Start())

	// close the inbox as the pool would on shutdown
	close(inbox)

	// internal waitgroup should clear
	Eventually(t, func() bool {
		c.wg.Wait()
		return true
	}, "wg should clear")

	// subscriptions remain open. Close() must be called to completely shut down
	Never(t, func() bool {
		_, ok := <-sub
		return !ok
	}, "sub should remain open")
}

func TestClerkClose(t *testing.T) {
	inbox, _ := mockInbox()
	c := makeClerk(inbox)

	subA, err := c.Subscribe(map[string]struct{}{"EVENT": {}}, 4)
	assert.NoError(t, err)
	subB, err := c.Subscribe(map[string]struct{}{"EOSE": {}}, 4)
	assert.NoError(t, err)

	assert.NoError(t, c.Start())

	c.Close()

	Eventually(t, func() bool {
		_, okA := <-subA
		_, okB := <-subB
		return !okA && !okB
	}, "all subscriber channels should be closed after Close()")
}
