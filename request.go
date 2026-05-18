package prism

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
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
	reqs map[string]*request

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
	id      string
	filters [][]byte

	isQuery bool
	active  bool

	buffer chan ReqEvent
	events chan ReqEvent
	closed chan ReqClosed

	once sync.Once
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

var encoder = base32.StdEncoding.WithPadding(base32.NoPadding)

func generateID() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generateID: %v", err))
	}

	return encoder.EncodeToString(b)
}

// ----------------------------------------------------------------------------
// Request Manager
// ----------------------------------------------------------------------------

func NewRequestManager(e *Envoy) *RequestManager {
	ctx, cancel := context.WithCancel(
		component.MustExtend(e.Context(), "request_manager"))

	m := &RequestManager{
		reqs: make(map[string]*request),

		envoy:  e,
		events: e.SubscribeEvents(),
		inbox:  e.SubscribeInbox([]string{"EVENT", "EOSE", "CLOSED"}),

		ctx:    ctx,
		cancel: cancel,
	}

	if h := e.Handler(); h != nil {
		comp := component.FromContext(ctx)
		m.handler = h
		m.logger = slog.New(h).With(slog.Any("component", comp))
	}

	m.wg.Add(2)
	go m.handleEvents()
	go m.routeInbox()

	return m
}

func (m *RequestManager) Stream(
	filters [][]byte,
) (string, <-chan ReqEvent, <-chan ReqClosed) {
	id := generateID()
	buffer := make(chan ReqEvent, 64)
	events := make(chan ReqEvent)
	closed := make(chan ReqClosed, 1)

	req := &request{
		id:      id,
		filters: filters,
		buffer:  buffer,
		events:  events,
		closed:  closed,
		isQuery: false,
	}

	m.mu.Lock()
	m.reqs[id] = req
	go func() {
		bufferedPipe(buffer, events)
		close(events)
	}()
	if m.envoy.IsConnected() {
		m.activateLock(req)
	}
	m.mu.Unlock()

	return id, events, closed
}

func (m *RequestManager) Query(
	filters [][]byte,
	timeout time.Duration,
) (events []ReqEvent, closed *ReqClosed) {
	if !m.envoy.IsConnected() {
		return nil, nil
	}

	id := generateID()
	eventsCh := make(chan ReqEvent)
	closedCh := make(chan ReqClosed, 1)

	req := &request{
		id:      id,
		filters: filters,
		buffer:  eventsCh,
		closed:  closedCh,
		isQuery: true,
	}

	m.mu.Lock()
	m.reqs[id] = req
	m.activateLock(req)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	var result []ReqEvent
	for {
		select {
		case ev, ok := <-eventsCh:
			if !ok {
				return result, nil
			}
			result = append(result, ev)
		case cl, ok := <-closedCh:
			if !ok {
				return result, nil
			}
			return result, &cl
		case <-ctx.Done():
			return result, nil
		}
	}
}

func (m *RequestManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	req, ok := m.reqs[id]
	if !ok {
		return fmt.Errorf("Cancel: unknown id %q", id)
	}

	if req.active {
		go m.envoy.Send(envelope.EncloseClose(id))
		req.active = false
	}

	req.once.Do(func() {
		close(req.buffer)
		close(req.closed)
	})
	delete(m.reqs, id)

	return nil
}

func (m *RequestManager) Close() {
	m.cancel()
	m.wg.Wait()

	m.mu.Lock()
	defer m.mu.Unlock()

	for id, req := range m.reqs {
		if req.active {
			go m.envoy.Send(envelope.EncloseClose(id))
		}
		req.once.Do(func() {
			close(req.buffer)
			close(req.closed)
		})
		delete(m.reqs, id)
	}
}

func (m *RequestManager) activateLock(req *request) {
	req.active = true
	go m.envoy.Send(envelope.EncloseReq(req.id, req.filters))
}

func (m *RequestManager) removeLock(req *request) {
	req.active = false
	req.once.Do(func() {
		close(req.buffer)
		close(req.closed)
	})
	delete(m.reqs, req.id)
}

func (m *RequestManager) handleEvents() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case ev, ok := <-m.events:
			if !ok {
				return
			}
			m.mu.Lock()
			switch ev.Kind {
			case EventConnected:
				for _, req := range m.reqs {
					if !req.isQuery {
						m.activateLock(req)
					}
				}
			case EventDisconnected:
				for _, req := range m.reqs {
					req.active = false
				}
			}
			m.mu.Unlock()
		}
	}
}

func (m *RequestManager) routeInbox() {
	defer m.wg.Done()

	for {
		select {
		case <-m.ctx.Done():
			return
		case msg, ok := <-m.inbox:
			if !ok {
				return
			}
			m.dispatchInbox(msg)
		}
	}
}

func (m *RequestManager) dispatchInbox(msg InboxMessage) {
	label, err := envelope.GetLabel(msg.Data)
	if err != nil {
		return
	}

	switch string(label) {
	case "EVENT":
		subID, event, err := envelope.FindSubscriptionEvent(msg.Data)
		if err != nil {
			return
		}
		m.mu.RLock()
		req, ok := m.reqs[subID]
		m.mu.RUnlock()
		if !ok {
			return
		}
		req.buffer <- ReqEvent{
			PeerID:     msg.ID,
			ReceivedAt: msg.ReceivedAt,
			Data:       event,
		}

	case "EOSE":
		subID, err := envelope.FindEOSE(msg.Data)
		if err != nil {
			return
		}
		m.mu.Lock()
		req, ok := m.reqs[subID]
		if !ok {
			m.mu.Unlock()
			return
		}
		if req.active && req.isQuery {
			m.removeLock(req)
			go m.envoy.Send(envelope.EncloseClose(subID))
		}
		m.mu.Unlock()

	case "CLOSED":
		subID, message, err := envelope.FindClosed(msg.Data)
		if err != nil {
			return
		}
		m.mu.Lock()
		req, ok := m.reqs[subID]
		if !ok {
			m.mu.Unlock()
			return
		}
		req.closed <- ReqClosed{
			PeerID:     msg.ID,
			ReceivedAt: msg.ReceivedAt,
			Data:       message,
		}
		m.removeLock(req)
		m.mu.Unlock()
	}
}
