package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

// Async Helpers

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

type mockPool struct {
	plugin  EmbassyPlugin
	ctx     context.Context
	url     string
	added   chan struct{}
	removed chan struct{}
	events  chan honeybee.OutboundPoolEvent
	inbox   chan honeybee.InboxMessage
	sent    chan []byte
}

// Mock Pool

func newMockPool(t *testing.T) *mockPool {
	t.Helper()

	ctx := component.MustNew(context.Background(), "prism", "test")
	url := "wss://test"

	added := make(chan struct{})
	removed := make(chan struct{})
	events := make(chan honeybee.OutboundPoolEvent, 16)
	inbox := make(chan honeybee.InboxMessage, 16)
	sent := make(chan []byte, 16)

	plugin := EmbassyPlugin{
		Connect: func(url string) error { close(added); return nil },
		Remove:  func(url string) error { close(removed); return nil },
		Send:    func(_ string, data []byte) error { sent <- data; return nil },
		Events:  events,
		Inbox:   inbox,
	}

	return &mockPool{
		plugin:  plugin,
		ctx:     ctx,
		url:     url,
		added:   added,
		removed: removed,
		events:  events,
		inbox:   inbox,
		sent:    sent,
	}
}

func (p *mockPool) connect() {
	p.events <- honeybee.OutboundPoolEvent{
		ID: p.url, Kind: honeybee.OutboundEventConnected, At: time.Now()}
}

func (p *mockPool) disconnect() {
	p.events <- honeybee.OutboundPoolEvent{
		ID: p.url, Kind: honeybee.OutboundEventDisconnected, At: time.Now()}
}

func (p *mockPool) receive(data []byte) {
	p.inbox <- honeybee.InboxMessage{
		ID:         p.url,
		Data:       data,
		ReceivedAt: time.Now(),
	}
}

// Mock request session harness

type mockSessionHarness struct {
	ctx            context.Context
	id             string
	filters        [][]byte
	req            []byte
	inbox          chan sessionMessage
	events         chan ReqEvent
	closed         chan ReqClosed
	closedOnce     *sync.Once
	done           chan struct{}
	sent           chan []byte
	send           func([]byte) error
	preterminate   func()
	terminatedWith chan terminateReason
	terminate      func(terminateReason)
}

func newMockSessionHarness() *mockSessionHarness {
	ctx := component.MustNew(context.Background(), "prism", "test")
	filters := [][]byte{[]byte(`{}`)}
	id := "TESTREQ"
	sent := make(chan []byte, 2)
	send := func(data []byte) error {
		sent <- data
		return nil
	}
	inbox := make(chan sessionMessage, 16)
	preterminate := func() { close(inbox) }
	terminatedWith := make(chan terminateReason, 1)
	terminate := func(r terminateReason) { terminatedWith <- r }

	return &mockSessionHarness{
		ctx:            ctx,
		id:             id,
		filters:        filters,
		req:            envelope.EncloseReq(id, filters),
		inbox:          inbox,
		events:         make(chan ReqEvent, 16),
		closed:         make(chan ReqClosed, 16),
		closedOnce:     &sync.Once{},
		done:           make(chan struct{}),
		sent:           sent,
		send:           send,
		preterminate:   preterminate,
		terminatedWith: terminatedWith,
		terminate:      terminate,
	}
}

// MockEnvoy

func newMockEnvoy(t *testing.T) (*mockPool, *Envoy) {
	t.Helper()

	p := newMockPool(t)
	emb := NewEmbassy(p.ctx, p.plugin, nil)
	err := emb.Dispatch(p.url)
	assert.NoError(t, err)
	envoy := emb.Call(p.url)
	return p, envoy
}
