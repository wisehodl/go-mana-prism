package prism

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-mana-prism/observer"
	"git.wisehodl.dev/jay/go-roots-ws"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

var (
	ErrMissedEOSE = errors.New("timeout: missed eose")
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type ReqEvent struct {
	Peer       string
	ReceivedAt time.Time
	Data       []byte
}

type ReqClosed struct {
	Peer       string
	ReceivedAt time.Time
	Data       string
}

// ----------------------------------------------------------------------------
// Observables
// ----------------------------------------------------------------------------

type ReqDispatched struct {
	ID           string
	DispatchedAt time.Time
}

type ReqSendFailed struct {
	ID  string
	Err error
	At  time.Time
}

type FirstEventReceived struct {
	ID         string
	ReceivedAt time.Time
}

type StreamEOSEReceived struct {
	ID         string
	ReceivedAt time.Time
}

type QueryEOSEReceived struct {
	ID         string
	ReceivedAt time.Time
}

type MissedEOSE struct {
	ID string
	At time.Time
}

type ClosedReceived struct {
	ID         string
	ReceivedAt time.Time
	Message    string
}

// ----------------------------------------------------------------------------
// ID Generation
// ----------------------------------------------------------------------------

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

func generateID() string {
	b := make([]byte, 5)
	if _, err := rand.Read(b); err != nil {
		panic(fmt.Sprintf("generateID: %v", err))
	}
	return b32.EncodeToString(b)
}

// ----------------------------------------------------------------------------
// Options
// ----------------------------------------------------------------------------

type RequestOption func(*requestOptions)

type requestOptions struct {
	id    string
	label string
}

// WithID sets an explicit subscription ID. Returns an error from Stream or
// Query if the ID is already in use.
func WithID(id string) RequestOption {
	return func(o *requestOptions) { o.id = id }
}

// WithLabel sets the prefix for the generated subscription ID. The default
// prefix is "req". The counter is shared across all labels.
func WithLabel(label string) RequestOption {
	return func(o *requestOptions) { o.label = label }
}

// ----------------------------------------------------------------------------
// Request Manager
// ----------------------------------------------------------------------------

type RequestManager struct {
	reqs map[string]*request

	envoy  *Envoy
	events <-chan PoolEvent
	inbox  <-chan InboxMessage

	ctx      context.Context
	cancel   context.CancelFunc
	mu       sync.RWMutex
	wg       sync.WaitGroup
	observer observer.Observer
	handler  slog.Handler
	logger   *slog.Logger
}

type request struct {
	id      string
	filters [][]byte

	isQuery        bool
	active         bool
	firstEventSeen bool

	buffer chan ReqEvent
	events chan ReqEvent
	closed chan ReqClosed

	deregisterOnce sync.Once
	closedOnce     sync.Once
}

func NewRequestManager(e *Envoy) *RequestManager {
	ctx, cancel := context.WithCancel(
		component.MustExtend(e.Context(), "request_manager"))

	m := &RequestManager{
		reqs: make(map[string]*request),

		envoy:  e,
		events: e.SubscribeEvents(),
		inbox:  e.SubscribeInbox([]string{"EVENT", "EOSE", "CLOSED"}),

		ctx:      ctx,
		cancel:   cancel,
		observer: e.Observer(),
	}

	if e.Handler() != nil {
		comp := component.FromContext(ctx)
		m.handler = e.Handler()
		m.logger = slog.New(m.handler).With(slog.Any("component", comp))
	}

	m.wg.Go(m.handleEvents)
	m.wg.Go(m.routeInbox)

	return m
}

func (m *RequestManager) Stream(
	filters [][]byte,
	opts ...RequestOption,
) (string, <-chan ReqEvent, <-chan ReqClosed, error) {
	id, events, closed, err := m.newStream(filters, false, opts...)
	return id, events, closed, err
}

func (m *RequestManager) newStream(
	filters [][]byte,
	isQuery bool,
	opts ...RequestOption,
) (string, <-chan ReqEvent, <-chan ReqClosed, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var o requestOptions
	for _, opt := range opts {
		opt(&o)
	}

	var id string
	if o.id != "" {
		if _, exists := m.reqs[o.id]; exists {
			return "", nil, nil, fmt.Errorf("Stream: id %q already in use", o.id)
		}
		id = o.id
	} else {
		label := o.label
		if label == "" {
			label = "REQ"
		}
		id = fmt.Sprintf("%s:%s", label, generateID())
	}
	buffer := make(chan ReqEvent, 64)
	closed := make(chan ReqClosed, 1)

	events := make(chan ReqEvent)
	go func() {
		bufferedPipe(buffer, events)
		close(events)
	}()

	req := &request{
		id:      id,
		filters: filters,
		buffer:  buffer,
		events:  events,
		closed:  closed,
		isQuery: isQuery,
	}

	m.reqs[id] = req
	if m.envoy.IsConnected() {
		m.activate(req)
	}

	return id, events, closed, nil
}

func (m *RequestManager) Query(
	filters [][]byte,
	timeout time.Duration,
	opts ...RequestOption,
) ([]ReqEvent, *ReqClosed, error) {
	id, eventsCh, closedCh, err := m.newStream(filters, true, opts...)
	if err != nil {
		return nil, nil, err
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	var result []ReqEvent
	for {
		select {
		case ev, ok := <-eventsCh:
			if !ok {
				return result, nil, nil
			}
			result = append(result, ev)
		case cl, ok := <-closedCh:
			if !ok {
				return result, nil, nil
			}
			return result, &cl, nil
		case <-ctx.Done():
			if ctx.Err() == context.DeadlineExceeded {
				// query timed out
				m.observer.Record(m.envoy.URL(),
					MissedEOSE{ID: id, At: time.Now()})
				if m.logger != nil {
					m.logger.Warn("missed eose", "req", id)
				}
				m.Cancel(id)
				return result, nil, ErrMissedEOSE
			}
			return result, nil, nil
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
		go func() {
			err := m.envoy.Send(envelope.EncloseClose(id))
			if err != nil {
				if m.logger != nil {
					m.logger.Warn("close send failed", "req", req.id, "error", err)
				}
				return
			}
			if m.logger != nil {
				m.logger.Debug("close sent", "req", req.id)
			}
		}()
		req.active = false
	}

	req.deregisterOnce.Do(func() {
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
		req.deregisterOnce.Do(func() {
			close(req.buffer)
			close(req.closed)
		})
		delete(m.reqs, id)
	}
}

func (m *RequestManager) activate(req *request) {
	req.active = true
	go func() {
		err := m.envoy.Send(envelope.EncloseReq(req.id, req.filters))
		if err != nil {
			m.observer.Record(m.envoy.URL(),
				ReqSendFailed{ID: req.id, Err: err, At: time.Now()})
			if m.logger != nil {
				m.logger.Warn("req send failed", "req", req.id, "error", err)
			}
			return
		}
		m.observer.Record(m.envoy.URL(),
			ReqDispatched{ID: req.id, DispatchedAt: time.Now()})
		if m.logger != nil {
			m.logger.Debug("req sent", "req", req.id)
		}
	}()
}

func (m *RequestManager) deregister(req *request) {
	req.active = false
	req.deregisterOnce.Do(func() {
		close(req.buffer)
		close(req.closed)
	})
	delete(m.reqs, req.id)
}

func (m *RequestManager) handleEvents() {
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
					m.activate(req)
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
		m.routeEvent(msg)
	case "EOSE":
		m.routeEOSE(msg)
	case "CLOSED":
		m.routeClosed(msg)
	}
}

// routeEvent, routeEOSE, and routeClosed use blocking sends into req.buffer,
// which reads eagerly into a slice buffer and cannot block the router.
func (m *RequestManager) routeEvent(msg InboxMessage) {
	subID, event, err := envelope.FindSubscriptionEvent(msg.Data)
	if err != nil {
		return
	}
	m.mu.Lock()
	req, ok := m.reqs[subID]
	if !ok {
		m.mu.Unlock()
		return
	}
	if !req.firstEventSeen {
		req.firstEventSeen = true
		reqSubID := req.id
		receivedAt := msg.ReceivedAt
		go m.observer.Record(m.envoy.URL(),
			FirstEventReceived{ID: reqSubID, ReceivedAt: receivedAt})
	}
	m.mu.Unlock()

	req.buffer <- ReqEvent{
		Peer:       msg.URL,
		ReceivedAt: msg.ReceivedAt,
		Data:       event,
	}
}

// routeEvent, routeEOSE, and routeClosed are always called sequentially from
// the same routeInbox goroutine via dispatchInbox. This makes it safe for
// routeEOSE to close req.buffer: no concurrent routeEvent send can race it.
func (m *RequestManager) routeEOSE(msg InboxMessage) {
	subID, err := envelope.FindEOSE(msg.Data)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.reqs[subID]
	if !ok {
		return
	}
	reqSubID := req.id
	receivedAt := msg.ReceivedAt
	if req.isQuery {
		go m.observer.Record(m.envoy.URL(),
			QueryEOSEReceived{ID: reqSubID, ReceivedAt: receivedAt})
	} else {
		go m.observer.Record(m.envoy.URL(),
			StreamEOSEReceived{ID: reqSubID, ReceivedAt: receivedAt})
	}
	if req.active && req.isQuery {
		// manually cleanup query state
		// specifically, do not close req.closed or events can be missed
		req.active = false
		close(req.buffer)
		delete(m.reqs, req.id)
		go func() {
			err := m.envoy.Send(envelope.EncloseClose(subID))
			if err != nil {
				if m.logger != nil {
					m.logger.Warn("close send failed", "req", req.id, "error", err)
				}
				return
			}
			if m.logger != nil {
				m.logger.Debug("close sent", "req", req.id)
			}
		}()
	}
}

func (m *RequestManager) routeClosed(msg InboxMessage) {
	subID, message, err := envelope.FindClosed(msg.Data)
	if err != nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	req, ok := m.reqs[subID]
	if !ok {
		return
	}
	reqSubID := req.id
	receivedAt := msg.ReceivedAt
	go m.observer.Record(m.envoy.URL(),
		ClosedReceived{
			ID:         reqSubID,
			ReceivedAt: receivedAt,
			Message:    message,
		})
	if m.logger != nil {
		m.logger.Warn("req closed by peer", "req", req.id, "message", message)
	}
	req.closedOnce.Do(func() {
		req.closed <- ReqClosed{
			Peer:       msg.URL,
			ReceivedAt: msg.ReceivedAt,
			Data:       message,
		}
	})
	m.deregister(req)
}
