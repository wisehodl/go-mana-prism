package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

const (
	TestTimeout         = 2 * time.Second
	TestTick            = 10 * time.Millisecond
	NegativeTestTimeout = 100 * time.Millisecond
)

func Eventually(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	assert.Eventually(t, condition, TestTimeout, TestTick, msg)
}

func Never(t *testing.T, condition func() bool, msg string) {
	t.Helper()
	assert.Never(t, condition, NegativeTestTimeout, TestTick, msg)
}

func mustEncloseReq(id string, filters [][]byte) []byte {
	return []byte(envelope.EncloseReq(id, filters))
}

// managerHarness wires up a real Envoy and RequestManager backed by
// controllable channels. Callers drive the envoy state and inbox by writing
// to the exported channels.
type managerHarness struct {
	envoy   *Envoy
	manager *RequestManager
	events  chan honeybee.OutboundPoolEvent
	inbox   chan honeybee.InboxMessage
	sent    chan []byte
}

func newManagerHarness(t *testing.T) *managerHarness {
	t.Helper()

	ctx := component.MustNew(context.Background(), "prism", "test")
	url := "wss://test"

	events := make(chan honeybee.OutboundPoolEvent, 4)
	inbox := make(chan honeybee.InboxMessage, 16)
	sent := make(chan []byte, 16)

	pool := EmbassyPlugin{
		Connect: func(string) error { return nil },
		Remove:  func(string) error { return nil },
		Send:    func(_ string, data []byte) error { sent <- data; return nil },
		Events:  events,
		Inbox:   inbox,
	}

	embassy := NewEmbassy(ctx, pool, nil)
	embassy.Dispatch(url)
	envoy := embassy.Call(url)

	manager := NewRequestManager(envoy)

	return &managerHarness{
		envoy:   envoy,
		manager: manager,
		events:  events,
		inbox:   inbox,
		sent:    sent,
	}
}

// connect simulates the envoy becoming connected.
func (h *managerHarness) connect() {
	h.events <- honeybee.OutboundPoolEvent{
		ID:   "wss://test",
		Kind: honeybee.OutboundEventConnected,
		At:   time.Now(),
	}
}

// disconnect simulates the envoy disconnecting.
func (h *managerHarness) disconnect() {
	h.events <- honeybee.OutboundPoolEvent{
		ID:   "wss://test",
		Kind: honeybee.OutboundEventDisconnected,
		At:   time.Now(),
	}
}

// sendEvent delivers an EVENT envelope for the given subID to the inbox.
func (h *managerHarness) sendEvent(subID string, eventData []byte) {
	h.inbox <- honeybee.InboxMessage{
		ID:         "wss://test",
		Data:       envelope.EncloseSubscriptionEvent(subID, eventData),
		ReceivedAt: time.Now(),
	}
}

// sendEOSE delivers an EOSE envelope for the given subID to the inbox.
func (h *managerHarness) sendEOSE(subID string) {
	h.inbox <- honeybee.InboxMessage{
		ID:         "wss://test",
		Data:       envelope.EncloseEOSE(subID),
		ReceivedAt: time.Now(),
	}
}

// sendClosed delivers a CLOSED envelope for the given subID to the inbox.
func (h *managerHarness) sendClosed(subID, message string) {
	h.inbox <- honeybee.InboxMessage{
		ID:         "wss://test",
		Data:       envelope.EncloseClosed(subID, message),
		ReceivedAt: time.Now(),
	}
}
