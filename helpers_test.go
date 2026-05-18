package prism

import (
	"context"
	honeybee "git.wisehodl.dev/jay/go-honeybee/outbound"
	"git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
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
	events  chan honeybee.PoolEvent
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
	events := make(chan honeybee.PoolEvent, 16)
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
	p.events <- honeybee.PoolEvent{
		ID: p.url, Kind: honeybee.EventConnected, At: time.Now()}
}

func (p *mockPool) disconnect() {
	p.events <- honeybee.PoolEvent{
		ID: p.url, Kind: honeybee.EventDisconnected, At: time.Now()}
}

func (p *mockPool) receive(data []byte) {
	p.inbox <- honeybee.InboxMessage{
		ID:         p.url,
		Data:       data,
		ReceivedAt: time.Now(),
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
