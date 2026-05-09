package prism

import (
	"context"
	"fmt"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type Envelope = []byte
type PoolSendFunc = func(id string, data Envelope) error

// Pool Plugins

type EmbassyPlugin struct {
	Connect func(id string) error
	Remove  func(id string) error
	Send    PoolSendFunc
	Events  <-chan honeybee.OutboundPoolEvent
}

type HotelPlugin struct {
	Add     func(id string, socket honeybee.Socket) error
	Replace func(id string, socket honeybee.Socket) error
	Remove  func(id string) error
	Send    PoolSendFunc
	Events  <-chan honeybee.InboundPoolEvent
}

// Events

type PoolEventKind = int

const (
	EventConnected PoolEventKind = iota
	EventDisconnected
	EventAdded
	EventRemoved
)

type PoolEvent struct {
	ID   string
	Kind PoolEventKind
	At   time.Time
}

func NewPoolEvent(id string, kind PoolEventKind, at time.Time) PoolEvent {
	return PoolEvent{ID: id, Kind: kind, At: at}
}

var convertPoolEvent = map[honeybee.OutboundPoolEventKind]PoolEventKind{
	honeybee.OutboundEventConnected:    EventConnected,
	honeybee.OutboundEventDisconnected: EventDisconnected,
}

// Adapter

type Adapter interface {
	Peers() []string
	HasPeer(id string) bool
	IsConnected(id string) bool
	Subscribe() <-chan PoolEvent
	Send(id string, data Envelope) error
}

// Embassy

type Embassy struct {
	pool      EmbassyPlugin
	peers     map[string]bool // peerID: isConnected
	journals  chan JournalEntry
	eventSubs []chan PoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

// Hotel

type Hotel struct {
	pool      HotelPlugin
	peers     map[string]bool // peerID: isConnected
	journals  chan JournalEntry
	eventSubs []chan PoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

// ----------------------------------------------------------------------------
// Embassy (Outbound Adapter)
// ----------------------------------------------------------------------------

func NewEmbassy(
	ctx context.Context,
	pool EmbassyPlugin,
	jc *JournalCollector,
	handler slog.Handler,
) *Embassy {
	ctx, cancel := context.WithCancel(
		component.MustNew(ctx, "prism", "embassy"))

	e := &Embassy{
		pool:      pool,
		peers:     make(map[string]bool),
		eventSubs: make([]chan PoolEvent, 0),
		ctx:       ctx,
		cancel:    cancel,
	}

	if jc != nil {
		e.journals = make(chan JournalEntry, 16)
		jc.Enroll(e.journals)
	}

	if handler != nil {
		c, ok := component.Get(ctx)
		if ok {
			e.logger = slog.New(handler).With(slog.Any("component", c))
		}
	}

	e.wg.Add(1)
	go e.runEventRouter()

	return e
}

func (e *Embassy) Dispatch(url string) error {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return fmt.Errorf("invalid url: %s", url)
	}

	if err := e.pool.Connect(url); err != nil {
		return fmt.Errorf("dispatch: %w", err)
	}

	e.mu.Lock()
	e.peers[url] = false
	subs := e.eventSubs
	e.mu.Unlock()

	for _, ch := range subs {
		select {
		case <-e.ctx.Done():
			return fmt.Errorf("closing")
		case ch <- NewPoolEvent(url, EventAdded, time.Now()):
		}
	}

	return nil
}

func (e *Embassy) Dismiss(url string) error {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return fmt.Errorf("invalid url: %s", url)
	}

	if err := e.pool.Remove(url); err != nil {
		return fmt.Errorf("dismiss: %w", err)
	}

	e.mu.Lock()
	delete(e.peers, url)
	subs := e.eventSubs
	e.mu.Unlock()

	for _, ch := range subs {
		select {
		case <-e.ctx.Done():
			return fmt.Errorf("closing")
		case ch <- NewPoolEvent(url, EventRemoved, time.Now()):
		}
	}

	return nil
}

func (e *Embassy) Close() {
	e.mu.Lock()
	peers := e.peers
	e.peers = make(map[string]bool)
	e.mu.Unlock()

	// dismiss peers
	for peer, _ := range peers {
		e.Dismiss(peer)
	}

	e.cancel()
	e.wg.Wait()

	e.mu.Lock()
	subs := e.eventSubs
	e.eventSubs = make([]chan PoolEvent, 0)
	e.mu.Unlock()

	// close subs
	for _, sub := range subs {
		close(sub)
	}
}

func (e *Embassy) Peers() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()

	peers := make([]string, 0, len(e.peers))
	for p, _ := range e.peers {
		peers = append(peers, p)
	}
	return peers
}

func (e *Embassy) HasPeer(url string) bool {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return false
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	_, ok := e.peers[url]
	return ok
}

func (e *Embassy) IsConnected(url string) bool {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return false
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	connected, _ := e.peers[url]
	return connected
}

func (e *Embassy) Subscribe() <-chan PoolEvent {
	e.mu.Lock()
	defer e.mu.Unlock()

	ch := make(chan PoolEvent, 16)
	e.eventSubs = append(e.eventSubs, ch)

	return ch
}

func (e *Embassy) Send(id string, data Envelope) error {
	return nil
}

// Internal

func (e *Embassy) runEventRouter() {
	defer e.wg.Done()

	for {
		select {
		case <-e.ctx.Done():
			return
		case ev, ok := <-e.pool.Events:
			if !ok {
				return
			}

			kind := convertPoolEvent[ev.Kind]

			e.mu.Lock()
			switch kind {
			case EventConnected:
				e.peers[ev.ID] = true
			case EventDisconnected:
				e.peers[ev.ID] = false
			}
			subs := e.eventSubs
			e.mu.Unlock()

			for _, ch := range subs {
				select {
				case <-e.ctx.Done():
				case ch <- NewPoolEvent(ev.ID, kind, ev.At):
				}
			}
		}
	}
}

// ----------------------------------------------------------------------------
// Hotel (Inbound Adapter)
// ----------------------------------------------------------------------------

func NewHotel() *Hotel {
	return nil
}

func (h *Hotel) Welcome(id string, socket honeybee.Socket) error {
	return nil
}

func (h *Hotel) WelcomeBack(id string, socket honeybee.Socket) error {
	return nil
}

func (h *Hotel) Farewell(id string) error {
	return nil
}

func (h *Hotel) Close() {}

func (h *Hotel) Peers() []string {
	return nil
}

func (h *Hotel) HasPeer(id string) bool {
	return false
}

func (h *Hotel) IsConnected(id string) bool {
	return false
}

func (h *Hotel) Subscribe() <-chan PoolEvent {
	return nil
}

func (h *Hotel) Send(id string, data Envelope) error {
	return nil
}
