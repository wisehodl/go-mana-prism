package prism

import (
	"context"
	"fmt"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-mana-prism/observer"
	"git.wisehodl.dev/jay/go-roots-ws"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type EmbassyPlugin struct {
	Connect func(url string, opts ...honeybee.ConnectOption) error
	Remove  func(url string) error
	Send    func(url string, data []byte) error
	Events  <-chan honeybee.PoolEvent
	Inbox   <-chan honeybee.InboxMessage
}

type EmbassyEventKind int

const (
	EventConnected EmbassyEventKind = iota
	EventDisconnected
	EventDialFailed
	EventRetired
	EventEmbassyUnknown
)

func mapEmbassyEvent(kind honeybee.PoolEventKind) EmbassyEventKind {
	switch kind {
	case honeybee.EventConnected:
		return EventConnected
	case honeybee.EventDisconnected:
		return EventDisconnected
	case honeybee.EventDialFailed:
		return EventDialFailed
	case honeybee.EventRetired:
		return EventRetired
	default:
		return EventEmbassyUnknown
	}
}

type PoolEvent struct {
	URL  string
	Kind EmbassyEventKind
	At   time.Time
	Err  error
}

type InboxMessage struct {
	URL        string
	Data       []byte
	ReceivedAt time.Time
}

// ----------------------------------------------------------------------------
// Options
// ----------------------------------------------------------------------------

type EmbassyConfig struct {
	handler  slog.Handler
	observer observer.Observer
}

type EmbassyOption func(*EmbassyConfig)

func WithEmbassyHandler(h slog.Handler) EmbassyOption {
	return func(c *EmbassyConfig) {
		c.handler = h
	}
}

func WithEmbassyObserver(o observer.Observer) EmbassyOption {
	return func(c *EmbassyConfig) {
		c.observer = o
	}
}

// ----------------------------------------------------------------------------
// Embassy
// ----------------------------------------------------------------------------

type Embassy struct {
	pool      EmbassyPlugin
	envoys    map[string]*Envoy
	eventSubs map[string]chan<- PoolEvent
	inboxSubs map[string]chan<- InboxMessage

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	wg       sync.WaitGroup
	observer observer.Observer
	handler  slog.Handler
	logger   *slog.Logger
}

func NewEmbassy(
	ctx context.Context,
	pool EmbassyPlugin,
	opts ...EmbassyOption,
) *Embassy {
	ctx, cancel := context.WithCancel(component.MustNew(ctx, "prism", "embassy"))
	cfg := EmbassyConfig{observer: observer.NullObserver{}}
	for _, o := range opts {
		o(&cfg)
	}

	e := &Embassy{
		pool:      pool,
		envoys:    make(map[string]*Envoy),
		eventSubs: make(map[string]chan<- PoolEvent),
		inboxSubs: make(map[string]chan<- InboxMessage),
		ctx:       ctx,
		cancel:    cancel,
		observer:  cfg.observer,
		handler:   cfg.handler,
	}

	if e.handler != nil {
		comp := component.FromContext(ctx)
		e.logger = slog.New(cfg.handler).With(slog.Any("component", comp))
	}

	e.wg.Go(e.routeEvents)
	e.wg.Go(e.routeInbox)

	return e
}

func (e *Embassy) Dispatch(url string) error {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return fmt.Errorf("invalid url: %w", err)
	}

	e.mu.Lock()
	_, exists := e.envoys[url]
	if exists {
		e.mu.Unlock()
		return fmt.Errorf("already dispatched: %s", url)
	}

	err = e.pool.Connect(url)
	if err != nil {
		e.mu.Unlock()
		return err
	}

	terminate := func() { e.dismiss(url) }
	send := func(data []byte) error { return e.send(url, data) }
	events := e.subscribeEventsLock(url)
	inbox := e.subscribeInboxLock(url)

	e.envoys[url] = newEnvoy(e.ctx, url, terminate, send, events, inbox, e.observer, e.handler)

	e.mu.Unlock()

	return nil
}

func (e *Embassy) Envoys() []string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	var envoys []string
	for url := range e.envoys {
		envoys = append(envoys, url)
	}
	return envoys
}

func (e *Embassy) Call(url string) *Envoy {
	url, err := honeybee.NormalizeURL(url)
	if err != nil {
		return nil
	}

	e.mu.RLock()
	defer e.mu.RUnlock()

	envoy, ok := e.envoys[url]
	if !ok {
		return nil
	}
	return envoy
}

func (e *Embassy) Close() {
	e.cancel()
	e.wg.Wait()
}

func (e *Embassy) dismiss(url string) {
	e.mu.Lock()
	defer e.mu.Unlock()

	e.pool.Remove(url)
	e.unsubscribeEventsLock(url)
	e.unsubscribeInboxLock(url)
	delete(e.envoys, url)
}

func (e *Embassy) send(url string, data []byte) error {
	return e.pool.Send(url, data)
}

func (e *Embassy) subscribeEventsLock(url string) <-chan PoolEvent {
	ch := make(chan PoolEvent)
	e.eventSubs[url] = ch
	return ch
}

func (e *Embassy) unsubscribeEventsLock(url string) {
	ch, ok := e.eventSubs[url]
	if !ok {
		return
	}
	close(ch)
	delete(e.eventSubs, url)
}

func (e *Embassy) subscribeInboxLock(url string) <-chan InboxMessage {
	ch := make(chan InboxMessage)
	e.inboxSubs[url] = ch
	return ch
}

func (e *Embassy) unsubscribeInboxLock(url string) {
	ch, ok := e.inboxSubs[url]
	if !ok {
		return
	}
	close(ch)
	delete(e.inboxSubs, url)
}

func (e *Embassy) routeEvents() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case ev, ok := <-e.pool.Events:
			if !ok {
				return
			}

			url, err := honeybee.NormalizeURL(ev.URL)
			if err != nil {
				continue
			}

			e.mu.RLock()
			sub, ok := e.eventSubs[url]
			e.mu.RUnlock()

			if !ok {
				continue
			}

			select {
			case <-e.ctx.Done():
				return
			case sub <- PoolEvent{
				URL: ev.URL, Kind: mapEmbassyEvent(ev.Kind), At: ev.At, Err: ev.Err,
			}:
			}
		}
	}
}

func (e *Embassy) routeInbox() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case ev, ok := <-e.pool.Inbox:
			if !ok {
				return
			}

			url, err := honeybee.NormalizeURL(ev.URL)
			if err != nil {
				continue
			}

			e.mu.RLock()
			sub, ok := e.inboxSubs[url]
			e.mu.RUnlock()

			if !ok {
				continue
			}

			select {
			case <-e.ctx.Done():
				return
			case sub <- InboxMessage{
				URL: ev.URL, Data: ev.Data, ReceivedAt: ev.ReceivedAt}:
			}
		}
	}
}

// ----------------------------------------------------------------------------
// Envoy
// ----------------------------------------------------------------------------

type Envoy struct {
	url               string
	connected         bool
	terminate         func()
	queue             chan []byte
	send              func(data []byte) error
	events            <-chan PoolEvent
	inbox             <-chan InboxMessage
	eventSubs         []chan PoolEvent
	labelledInboxSubs map[string][]chan InboxMessage
	inboxSubs         []chan InboxMessage

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	wg       sync.WaitGroup
	observer observer.Observer
	handler  slog.Handler
	logger   *slog.Logger
}

func newEnvoy(
	ctx context.Context,
	url string,
	terminate func(),
	send func(data []byte) error,
	events <-chan PoolEvent,
	inbox <-chan InboxMessage,
	observer observer.Observer,
	handler slog.Handler,
) *Envoy {
	ctx, cancel := context.WithCancel(component.MustExtend(ctx, "envoy"))

	e := &Envoy{
		url:               url,
		terminate:         terminate,
		queue:             make(chan []byte),
		send:              send,
		events:            events,
		inbox:             inbox,
		labelledInboxSubs: make(map[string][]chan InboxMessage),
		ctx:               ctx,
		cancel:            cancel,
		observer:          observer,
	}

	if handler != nil {
		comp := component.FromContext(ctx)
		e.handler = handler.WithAttrs([]slog.Attr{slog.String("peer", url)})
		e.logger = slog.New(e.handler).With(slog.Any("component", comp))
	}

	e.wg.Go(e.publishEvents)
	e.wg.Go(e.routeInbox)

	return e
}

func (e *Envoy) IsConnected() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.connected
}

func (e *Envoy) Context() context.Context {
	return e.ctx
}

func (e *Envoy) URL() string {
	return e.url
}

func (e *Envoy) Handler() slog.Handler {
	return e.handler
}

func (e *Envoy) Observer() observer.Observer {
	return e.observer
}

func (e *Envoy) Dismiss() {
	e.terminate()
	e.cancel()
	e.wg.Wait()

	e.mu.Lock()
	defer e.mu.Unlock()

	for _, sub := range e.eventSubs {
		close(sub)
	}

	for _, sub := range e.inboxSubs {
		close(sub)
	}

	e.eventSubs = nil
	e.inboxSubs = nil
	e.labelledInboxSubs = make(map[string][]chan InboxMessage)
}

func (e *Envoy) Send(data []byte) error {
	return e.send(data)
}

func (e *Envoy) SubscribeEvents() <-chan PoolEvent {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan PoolEvent)
	e.eventSubs = append(e.eventSubs, ch)
	return ch
}

func (e *Envoy) UnsubscribeEvents(ch <-chan PoolEvent) {
	e.mu.Lock()
	defer e.mu.Unlock()
	for i, sub := range e.eventSubs {
		if sub == ch {
			e.eventSubs[i] = e.eventSubs[len(e.eventSubs)-1]
			e.eventSubs = e.eventSubs[:len(e.eventSubs)-1]
			return
		}
	}
}

func (e *Envoy) SubscribeInbox(labels []string) <-chan InboxMessage {
	e.mu.Lock()
	defer e.mu.Unlock()
	ch := make(chan InboxMessage)
	e.inboxSubs = append(e.inboxSubs, ch)
	for _, label := range labels {
		if _, ok := e.labelledInboxSubs[label]; !ok {
			e.labelledInboxSubs[label] = make([]chan InboxMessage, 0)
		}
		e.labelledInboxSubs[label] = append(e.labelledInboxSubs[label], ch)
	}
	return ch
}

func (e *Envoy) publishEvents() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case ev, ok := <-e.events:
			if !ok {
				return
			}

			e.mu.Lock()
			switch ev.Kind {
			case EventConnected:
				e.connected = true
			case EventDisconnected:
				e.connected = false
			}
			subs := e.eventSubs
			e.mu.Unlock()

			for _, ch := range subs {
				select {
				case <-e.ctx.Done():
					return
				case ch <- ev:
				}
			}
		}
	}
}

func (e *Envoy) routeInbox() {
	for {
		select {
		case <-e.ctx.Done():
			return
		case msg, ok := <-e.inbox:
			if !ok {
				return
			}

			label, err := envelope.GetLabel(msg.Data)
			if err != nil {
				continue
			}

			e.mu.RLock()
			subs, ok := e.labelledInboxSubs[string(label)]
			e.mu.RUnlock()

			if !ok {
				continue
			}

			for _, ch := range subs {
				select {
				case <-e.ctx.Done():
					return
				case ch <- msg:
				}
			}
		}
	}
}
