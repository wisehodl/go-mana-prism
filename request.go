package prism

import (
	"context"
	"encoding/base32"
	"fmt"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type ReqEvent struct {
	PeerID     string
	ReceivedAt time.Time
	Data       []byte
}

type ReqClosed struct {
	PeerID     string
	ReceivedAt time.Time
	Data       string
}

type RequestManager struct {
	reqs      map[string][][]byte
	inboxSubs map[string]chan<- InboxMessage
	done      chan struct{}
	reqWg     sync.WaitGroup

	envoy  *Envoy
	events <-chan OutboundPoolEvent
	inbox  <-chan InboxMessage

	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
	wg      sync.WaitGroup
	handler slog.Handler
	logger  *slog.Logger
}

type request struct {
	id    string
	req   []byte
	query bool

	inbox     <-chan InboxMessage
	stop      chan struct{}
	terminate func()

	events chan ReqEvent
	closed chan ReqClosed

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	logger *slog.Logger
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

var encoder = base32.StdEncoding.WithPadding(base32.NoPadding)

func generateID() string {
	return ""
}

// ----------------------------------------------------------------------------
// Request Manager
// ----------------------------------------------------------------------------

func NewRequestManager(envoy *Envoy) *RequestManager {
	ctx, cancel := context.WithCancel(
		component.MustExtend(envoy.Context(), "request_manager"))

	m := &RequestManager{
		reqs:   make(map[string]*request),
		envoy:  envoy,
		events: envoy.SubscribeEvents(),
		inbox:  envoy.SubscribeInbox([]string{"EVENT", "EOSE", "CLOSED"}),
		ctx:    ctx,
		cancel: cancel,
	}

	if h := envoy.Handler(); h != nil {
		comp := component.FromContext(ctx)
		m.handler = h
		m.logger = slog.New(h).With(slog.Any("component", comp))
	}

	m.wg.Add(1)
	go m.handleEvents()

	return m
}

func (m *RequestManager) Stream(
	filters [][]byte,
) (
	reqID string,
	events <-chan ReqEvent,
	closed <-chan ReqClosed,
) {
	ctx := component.MustExtend(m.ctx, "stream")
	id := generateID()
	terminate := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.unsubscribeInboxLock(id)
		delete(m.reqs, id)
		m.reqWg.Done()
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.reqWg.Add(1)
	r := newStreamRequest(ctx, id, envelope.EncloseReq(id, filters),
		m.subscribeInboxLock(id), m.done, terminate, m.handler)

	m.reqs[id] = r

	return id, r.Events(), r.Closed()
}

func (m *RequestManager) Query(
	filters [][]byte,
	timeout time.Duration,
) (
	events []ReqEvent,
	closed *ReqClosed,
) {
	ctx, _ := context.WithTimeout(component.MustExtend(m.ctx, "query"), timeout)

	id := generateID()
	terminate := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		m.unsubscribeInboxLock(id)
	}

	m.mu.Lock()
	r := newQueryRequest(ctx, id, envelope.EncloseReq(id, filters),
		m.subscribeInboxLock(id), m.done, terminate, m.handler)
	m.mu.Unlock()

	for {
		select {
		case <-m.ctx.Done():
			return
		case rEvent, ok := <-r.Events():
			if !ok {
				return
			}
			events = append(events, rEvent)
		case rClosed := <-r.Closed():
			closed = &rClosed
			return
		}
	}

}

func (m *RequestManager) Cancel(id string) error {
	req, ok := m.reqs[id]
	if !ok {
		return fmt.Errorf("req not found: %s", id)
	}
	req.Close()
	return nil
}

func (m *RequestManager) Close() {
	m.cancel()
	m.wg.Wait()
}

func (m *RequestManager) start() {

}

func (m *RequestManager) stop() {

}

func (m *RequestManager) subscribeInboxLock(id string) <-chan InboxMessage {
	ch := make(chan InboxMessage)
	m.inboxSubs[id] = ch
	return ch
}

func (m *RequestManager) unsubscribeInboxLock(id string) {
	ch, ok := m.inboxSubs[id]
	if !ok {
		return
	}
	close(ch)
	delete(m.inboxSubs, id)
}

func (m *RequestManager) handleEvents() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case ev := <-m.events:
			switch ev.Kind {
			case EventConnected:
				m.start()

			case EventDisconnected:
				m.stop()
			}
		}
	}
}

func (m *RequestManager) routeInbox() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case ev, ok := <-m.inbox:
			if !ok {
				return
			}

			url, err := honeybee.NormalizeURL(ev.ID)
			if err != nil {
				continue
			}

			m.mu.RLock()
			sub, ok := m.inboxSubs[url]
			m.mu.RUnlock()

			if !ok {
				continue
			}

			select {
			case <-m.ctx.Done():
				return
			case sub <- ev:
			}
		}
	}
}

// ----------------------------------------------------------------------------
// Request
// ----------------------------------------------------------------------------

func newStreamRequest(
	ctx context.Context,
	id string,
	req []byte,
	inbox <-chan InboxMessage,
	stop chan struct{},
	terminate func(),
	handler slog.Handler,
) *request {
	ctx, cancel := context.WithCancel(component.MustExtend(ctx, "request"))

	r := &request{
		id:        id,
		req:       req,
		query:     false,
		inbox:     inbox,
		stop:      stop,
		terminate: terminate,
		ctx:       ctx,
		cancel:    cancel,
	}

	if handler != nil {
		comp := component.FromContext(ctx)
		r.logger = slog.New(handler).With(slog.Any("component", comp))
	}

	return r
}

func newQueryRequest(
	ctx context.Context,
	id string,
	req []byte,
	inbox <-chan InboxMessage,
	stop chan struct{},
	terminate func(),
	handler slog.Handler,
) *request {
	ctx, cancel := context.WithCancel(component.MustExtend(ctx, "request"))

	r := &request{
		id:        id,
		req:       req,
		query:     true,
		inbox:     inbox,
		stop:      stop,
		terminate: terminate,
		ctx:       ctx,
		cancel:    cancel,
	}

	if handler != nil {
		comp := component.FromContext(ctx)
		r.logger = slog.New(handler).With(slog.Any("component", comp))
	}

	return r
}

func (r *request) Close() {
	r.cancel()
	r.wg.Wait()
	r.terminate()
}

func (r *request) Events() <-chan ReqEvent {
	return r.events
}

func (r *request) Closed() <-chan ReqClosed {
	return r.closed
}
