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
	reqs      map[string]*request
	sessions  map[string]*session
	inboxSubs map[string]*sessionSub
	done      chan struct{}
	sessionWg sync.WaitGroup

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
	id             string
	filters        [][]byte
	buffer         chan ReqEvent
	events         chan ReqEvent
	closed         chan ReqClosed
	deregisterOnce sync.Once
	closedOnce     sync.Once
}

type session struct {
	id  string
	req []byte

	eose   <-chan struct{}
	closed <-chan struct{}

	done        chan struct{}
	send        func([]byte) error
	terminate   func(terminateReason)
	closeOnEOSE bool

	ctx    context.Context
	cancel context.CancelFunc
	logger *slog.Logger
}

type sessionSub struct {
	eose       chan<- struct{}
	closed     chan<- struct{}
	eoseOnce   sync.Once
	closedOnce sync.Once
}

type terminateReason int

const (
	termSendFailed terminateReason = iota
	termCloseSent
	termReceivedClosed
	termDone
	termCancelled
)

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
		reqs:      make(map[string]*request),
		sessions:  make(map[string]*session),
		inboxSubs: make(map[string]*sessionSub),

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

	// start event handler
	m.wg.Add(1)
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
	}

	m.mu.Lock()
	m.reqs[id] = req
	go func() {
		bufferedPipe(buffer, events)
		close(events)
	}()
	if m.envoy.IsConnected() {
		m.spawnSession(req, false)
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
	buffer := make(chan ReqEvent, 64)
	eventsCh := make(chan ReqEvent)
	closedCh := make(chan ReqClosed, 1)

	req := &request{
		id:      id,
		filters: filters,
		buffer:  buffer,
		events:  eventsCh,
		closed:  closedCh,
	}

	m.mu.Lock()
	m.reqs[id] = req
	go func() {
		bufferedPipe(buffer, eventsCh)
		close(eventsCh)
	}()

	m.spawnSession(req, true)
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

	if sess, ok := m.sessions[id]; ok {
		sess.Close()
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

	m.mu.RLock()
	sessions := make(map[string]*session)
	for id, s := range m.sessions {
		sessions[id] = s
	}
	m.mu.RUnlock()

	for _, sess := range sessions {
		sess.Close()
	}

	m.sessionWg.Wait()

	m.mu.Lock()
	for id, req := range m.reqs {
		req.deregisterOnce.Do(func() {
			close(req.buffer)
			close(req.closed)
		})
		delete(m.reqs, id)
	}
	m.mu.Unlock()
}

func (m *RequestManager) spawnSession(req *request, query bool) {
	eose := make(chan struct{})
	closed := make(chan struct{})

	sub := &sessionSub{eose: eose, closed: closed}
	m.inboxSubs[req.id] = sub

	var once sync.Once
	terminate := func(r terminateReason) {
		once.Do(func() {
			m.mu.Lock()
			close(eose)
			close(closed)
			delete(m.inboxSubs, req.id)
			delete(m.sessions, req.id)
			m.mu.Unlock()
			m.sessionWg.Done()
			if r == termReceivedClosed || r == termCloseSent {
				req.deregisterOnce.Do(func() {
					close(req.buffer)
					close(req.closed)
				})
				m.mu.Lock()
				delete(m.reqs, req.id)
				m.mu.Unlock()
			}
		})
	}

	req_env := envelope.EncloseReq(req.id, req.filters)
	sess := newSession(
		m.ctx, req.id, req_env,
		eose, closed, m.done,
		m.envoy.Send, terminate,
		query, m.handler,
	)
	m.sessions[req.id] = sess
	m.sessionWg.Add(1)
	go sess.run()
}

func (m *RequestManager) start() {
	// start all request sessions
}

func (m *RequestManager) stop() {
	// stop all running sessions
}

func (m *RequestManager) handleEvents() {
	defer m.wg.Done()

	// start/stop sessions on connect/disconnect
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
		select {
		case req.buffer <- ReqEvent{
			PeerID: msg.ID, ReceivedAt: msg.ReceivedAt, Data: event}:
		}
	case "EOSE":
		subID, err := envelope.FindEOSE(msg.Data)
		if err != nil {
			return
		}
		m.mu.RLock()
		sub, ok := m.inboxSubs[subID]
		m.mu.RUnlock()
		if !ok {
			return
		}
		sub.eoseOnce.Do(func() {
			select {
			case sub.eose <- struct{}{}:
			default:
			}
		})
	case "CLOSED":
		subID, message, err := envelope.FindClosed(msg.Data)
		if err != nil {
			return
		}
		m.mu.RLock()
		req, reqOk := m.reqs[subID]
		sub, subOk := m.inboxSubs[subID]
		m.mu.RUnlock()
		if reqOk {
			req.closedOnce.Do(func() {
				req.closed <- ReqClosed{
					PeerID: msg.ID, ReceivedAt: msg.ReceivedAt, Data: message}
			})
		}
		if subOk {
			sub.closedOnce.Do(func() {
				sub.closed <- struct{}{}
			})
		}
	}
}

// ----------------------------------------------------------------------------
// Session
// ----------------------------------------------------------------------------

func newSession(
	ctx context.Context,
	id string,
	req []byte,
	eose <-chan struct{},
	closed <-chan struct{},
	done chan struct{},
	send func(data []byte) error,
	terminate func(terminateReason),
	isQuery bool,
	handler slog.Handler,
) *session {
	ctx, cancel := context.WithCancel(component.MustExtend(ctx, "session"))
	s := &session{
		id:          id,
		req:         req,
		eose:        eose,
		closed:      closed,
		done:        done,
		send:        send,
		terminate:   terminate,
		closeOnEOSE: isQuery,
		ctx:         ctx,
		cancel:      cancel,
	}
	// create logger if handler is supplied
	return s
}

func (s *session) run() {
	// send initial REQ; goroutine allows done/ctx cancellation to abort the wait
	sent := make(chan error, 1)
	go func() { sent <- s.send(s.req) }()

	for {
		select {
		case err := <-sent:
			if err != nil {
				s.terminate(termSendFailed)
				return
			}
		case <-s.done:
			s.terminate(termDone)
			return
		case <-s.ctx.Done():
			s.send(envelope.EncloseClose(s.id))
			s.terminate(termCancelled)
			return
		case <-s.eose:
			if s.closeOnEOSE {
				s.send(envelope.EncloseClose(s.id))
				s.terminate(termCloseSent)
				return
			}
		case <-s.closed:
			s.terminate(termReceivedClosed)
			return
		}
	}
}

func (s *session) Close() {
	s.cancel()
}
