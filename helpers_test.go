package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Async Helpers
// ----------------------------------------------------------------------------

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

// ----------------------------------------------------------------------------
// Mock Pool
// ----------------------------------------------------------------------------

type mockPool struct {
	plugin  EmbassyPlugin
	ctx     context.Context
	url     string
	added   chan struct{}
	removed chan struct{}
	events  chan honeybee.PoolEvent
	inbox   chan honeybee.InboxMessage
	sent    chan []byte
	sendMu  sync.Mutex
	sendErr error
}

func newMockPool(t *testing.T) *mockPool {
	t.Helper()

	p := &mockPool{}

	p.ctx = component.MustNew(context.Background(), "prism", "test")
	p.url = "wss://test"

	p.added = make(chan struct{})
	p.removed = make(chan struct{})
	p.events = make(chan honeybee.PoolEvent, 16)
	p.inbox = make(chan honeybee.InboxMessage, 16)
	p.sent = make(chan []byte, 16)
	sendFunc := func(_ string, data []byte) error {
		p.sendMu.Lock()
		err := p.sendErr
		p.sendMu.Unlock()
		if err != nil {
			return err
		}
		p.sent <- data
		return nil
	}

	p.plugin = EmbassyPlugin{
		Connect: func(url string, opts ...honeybee.ConnectOption) error {
			close(p.added)
			return nil
		},
		Remove: func(url string) error { close(p.removed); return nil },
		Send:   sendFunc,
		Events: p.events,
		Inbox:  p.inbox,
	}

	return p
}

func (p *mockPool) connect() {
	p.events <- honeybee.PoolEvent{
		URL: p.url, Kind: honeybee.EventConnected, At: time.Now()}
}

func (p *mockPool) disconnect() {
	p.events <- honeybee.PoolEvent{
		URL: p.url, Kind: honeybee.EventDisconnected, At: time.Now()}
}

func (p *mockPool) dialFail(err error) {
	p.events <- honeybee.PoolEvent{
		URL: p.url, Kind: honeybee.EventDialFailed, Err: err, At: time.Now()}
}

func (p *mockPool) receive(data []byte) {
	p.inbox <- honeybee.InboxMessage{
		URL:        p.url,
		Data:       data,
		ReceivedAt: time.Now(),
	}
}

func (p *mockPool) setSendError(err error) {
	p.sendMu.Lock()
	defer p.sendMu.Unlock()
	p.sendErr = err
}

// ----------------------------------------------------------------------------
// MockEnvoy
// ----------------------------------------------------------------------------

func newMockEnvoy(t *testing.T, opts ...EmbassyOption) (*mockPool, *Envoy) {
	t.Helper()

	p := newMockPool(t)
	emb := NewEmbassy(p.ctx, p.plugin, opts...)
	err := emb.Dispatch(p.url)
	assert.NoError(t, err)
	envoy := emb.Call(p.url)
	return p, envoy
}

// ----------------------------------------------------------------------------
// MockObserver
// ----------------------------------------------------------------------------

type observerRecord struct {
	peerID string
	event  any
}

type mockObserver struct {
	mu      sync.Mutex
	records []observerRecord
}

func (o *mockObserver) Record(peerID string, event any) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.records = append(o.records, observerRecord{peerID, event})
}

// EventsOf returns all recorded events of type T.
func EventsOf[T any](o *mockObserver) []T {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []T
	for _, r := range o.records {
		if v, ok := r.event.(T); ok {
			out = append(out, v)
		}
	}
	return out
}
