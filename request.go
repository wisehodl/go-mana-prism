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
// Parsed inbox message types
// ----------------------------------------------------------------------------

type inboxEvent struct {
	subID      string
	data       []byte
	receivedAt time.Time
}

type inboxEOSE struct {
	subID      string
	receivedAt time.Time
}

type inboxClosed struct {
	subID      string
	message    string
	receivedAt time.Time
}

// ----------------------------------------------------------------------------
// Session inbox (per-session typed channels)
// ----------------------------------------------------------------------------

type sessionInbox struct {
	events chan inboxEvent
	eose   chan inboxEOSE
	closed chan inboxClosed
}

const sessionInboxBuffer = 64

func newSessionInbox() *sessionInbox {
	return &sessionInbox{
		events: make(chan inboxEvent, sessionInboxBuffer),
		eose:   make(chan inboxEOSE, 1),
		closed: make(chan inboxClosed, 1),
	}
}

// ----------------------------------------------------------------------------
// Registration (durable subscription identity)
// ----------------------------------------------------------------------------

type registration struct {
	filters    [][]byte
	eventsIn   chan ReqEvent
	eventsOut  <-chan ReqEvent
	closed     chan ReqClosed
	deregister sync.Once
}

// ----------------------------------------------------------------------------
// Output types
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

// ----------------------------------------------------------------------------
// Session options
// ----------------------------------------------------------------------------

type sessionOptions struct {
	eoseClose     bool
	deregister    func()
	inbox         *sessionInbox
	forwardEvents chan<- ReqEvent
	forwardClosed chan<- ReqClosed
}

type SessionOption func(*sessionOptions)

func withEOSEClose() SessionOption {
	return func(o *sessionOptions) {
		o.eoseClose = true
	}
}

func withDeregister(fn func()) SessionOption {
	return func(o *sessionOptions) {
		o.deregister = fn
	}
}

func withSessionInbox(si *sessionInbox) SessionOption {
	return func(o *sessionOptions) {
		o.inbox = si
	}
}

func withForwardEvents(ch chan<- ReqEvent) SessionOption {
	return func(o *sessionOptions) {
		o.forwardEvents = ch
	}
}

func withForwardClosed(ch chan<- ReqClosed) SessionOption {
	return func(o *sessionOptions) {
		o.forwardClosed = ch
	}
}

// ----------------------------------------------------------------------------
// Session
// ----------------------------------------------------------------------------

type session struct {
	id             string
	req            []byte
	send           func([]byte) error
	done           <-chan struct{}
	terminate      func()
	deregister     func()
	eoseClose      bool
	inbox          *sessionInbox
	forwardEvents  chan<- ReqEvent
	forwardClosed  chan<- ReqClosed

	ctx    context.Context
	cancel context.CancelFunc
	once   sync.Once
}

func newSession(
	id string,
	req []byte,
	send func([]byte) error,
	done <-chan struct{},
	terminate func(),
	opts ...SessionOption,
) *session {
	o := &sessionOptions{
		deregister: func() {},
	}
	for _, opt := range opts {
		opt(o)
	}

	ctx, cancel := context.WithCancel(context.Background())

	return &session{
		id:            id,
		req:           req,
		send:          send,
		done:          done,
		terminate:     terminate,
		deregister:    o.deregister,
		eoseClose:     o.eoseClose,
		inbox:         o.inbox,
		forwardEvents: o.forwardEvents,
		forwardClosed: o.forwardClosed,
		ctx:           ctx,
		cancel:        cancel,
	}
}

func (s *session) run() {
	defer s.exit()

	// Send step: launch send in goroutine, wait for result or done.
	sent := make(chan error, 1)
	go func() { sent <- s.send(s.req) }()

	select {
	case <-s.done:
		return
	case <-s.ctx.Done():
		return
	case err := <-sent:
		if err != nil {
			return
		}
	}

	if s.inbox == nil {
		return
	}

	// Message loop.
	for {
		select {
		case <-s.done:
			return
		case <-s.ctx.Done():
			s.send(envelope.EncloseClose(s.id)) //nolint:errcheck
			return
		case ev, ok := <-s.inbox.events:
			if !ok {
				return
			}
			if s.forwardEvents != nil {
				select {
				case <-s.done:
					return
				case <-s.ctx.Done():
					return
				case s.forwardEvents <- ReqEvent{ReceivedAt: ev.receivedAt, Data: ev.data}:
				}
			}
		case _, ok := <-s.inbox.eose:
			if !ok {
				return
			}
			if s.eoseClose {
				// Drain buffered events before closing.
				for {
					select {
					case ev, ok := <-s.inbox.events:
						if !ok {
							s.send(envelope.EncloseClose(s.id)) //nolint:errcheck
							return
						}
						if s.forwardEvents != nil {
							select {
							case <-s.done:
								return
							case <-s.ctx.Done():
								return
							case s.forwardEvents <- ReqEvent{ReceivedAt: ev.receivedAt, Data: ev.data}:
							}
						}
					default:
						s.send(envelope.EncloseClose(s.id)) //nolint:errcheck
						return
					}
				}
			}
		case cl, ok := <-s.inbox.closed:
			if !ok {
				return
			}
			if s.forwardClosed != nil {
				select {
				case <-s.done:
				case <-s.ctx.Done():
				case s.forwardClosed <- ReqClosed{ReceivedAt: cl.receivedAt, Data: cl.message}:
				}
			}
			s.doDeregister()
			return
		}
	}
}

func (s *session) exit() {
	s.once.Do(func() {
		s.terminate()
	})
}

func (s *session) doDeregister() {
	s.once.Do(func() {
		s.terminate()
	})
	s.deregister()
}

func (s *session) Close() {
	s.cancel()
}

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

var encoder = base32.StdEncoding.WithPadding(base32.NoPadding)

func generateID() string {
	b := make([]byte, 5)
	_, err := rand.Read(b)
	if err != nil {
		panic(fmt.Sprintf("generateID: %v", err))
	}
	return encoder.EncodeToString(b)
}

// ----------------------------------------------------------------------------
// Request Manager
// ----------------------------------------------------------------------------

type RequestManager struct {
	regs      map[string]*registration
	sessions  map[string]*session
	inboxSubs map[string]*sessionInbox
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

func NewRequestManager(envoy *Envoy) *RequestManager {
	ctx, cancel := context.WithCancel(
		component.MustExtend(envoy.Context(), "request_manager"))

	m := &RequestManager{
		regs:      make(map[string]*registration),
		sessions:  make(map[string]*session),
		inboxSubs: make(map[string]*sessionInbox),
		envoy:     envoy,
		events:    envoy.SubscribeEvents(),
		inbox:     envoy.SubscribeInbox([]string{"EVENT", "EOSE", "CLOSED"}),
		ctx:       ctx,
		cancel:    cancel,
	}

	if h := envoy.Handler(); h != nil {
		comp := component.FromContext(ctx)
		m.handler = h
		m.logger = slog.New(h).With(slog.Any("component", comp))
	}

	m.wg.Add(2)
	go m.handleEvents()
	go m.routeInbox()

	return m
}

func (m *RequestManager) Stream(filters [][]byte) (string, <-chan ReqEvent, <-chan ReqClosed) {
	id := generateID()

	evIn := make(chan ReqEvent)
	evOut := make(chan ReqEvent)
	cl := make(chan ReqClosed, 1)

	reg := &registration{
		filters:  filters,
		eventsIn: evIn,
		closed:   cl,
	}

	go bufferedPipe(evIn, evOut)
	reg.eventsOut = evOut

	m.mu.Lock()
	m.regs[id] = reg
	if m.envoy.IsConnected() {
		m.spawnSessionLock(id, reg)
	}
	m.mu.Unlock()

	return id, reg.eventsOut, reg.closed
}

func (m *RequestManager) Query(filters [][]byte, timeout time.Duration) ([]ReqEvent, *ReqClosed) {
	if !m.envoy.IsConnected() {
		return nil, nil
	}

	ctx, cancel := context.WithTimeout(m.ctx, timeout)
	defer cancel()

	id := generateID()
	si := newSessionInbox()

	// Buffered collection channels so the session can forward without blocking.
	evCh := make(chan ReqEvent, sessionInboxBuffer)
	clCh := make(chan ReqClosed, 1)
	sessionDone := make(chan struct{})

	m.mu.Lock()
	m.inboxSubs[id] = si
	m.mu.Unlock()

	terminate := func() {
		m.mu.Lock()
		delete(m.inboxSubs, id)
		m.mu.Unlock()
		m.reqWg.Done()
		close(sessionDone)
	}

	m.reqWg.Add(1)
	s := newSession(
		id,
		envelope.EncloseReq(id, filters),
		m.envoy.Send,
		m.done,
		terminate,
		withEOSEClose(),
		withSessionInbox(si),
		withForwardEvents(evCh),
		withForwardClosed(clCh),
	)
	go s.run()

	var events []ReqEvent
	var closed *ReqClosed

	// Wait for the session to finish, or timeout.
	select {
	case <-ctx.Done():
		s.Close()
		<-sessionDone
	case <-sessionDone:
	}

	// Drain whatever the session forwarded.
	for {
		select {
		case ev := <-evCh:
			events = append(events, ev)
		default:
			goto drained
		}
	}
drained:
	select {
	case cl := <-clCh:
		closed = &cl
	default:
	}

	return events, closed
}

func (m *RequestManager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	s, ok := m.sessions[id]
	if !ok {
		return fmt.Errorf("session not found: %s", id)
	}
	s.Close()
	return nil
}

func (m *RequestManager) Close() {
	m.cancel()
	m.wg.Wait()
}

func (m *RequestManager) spawnSessionLock(id string, reg *registration) {
	si := newSessionInbox()
	m.inboxSubs[id] = si

	terminate := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		delete(m.inboxSubs, id)
		delete(m.sessions, id)
		m.reqWg.Done()
	}

	deregister := func() {
		m.mu.Lock()
		defer m.mu.Unlock()
		reg.deregister.Do(func() {
			delete(m.regs, id)
			close(reg.eventsIn)
			reg.closed <- ReqClosed{}
			close(reg.closed)
		})
	}

	m.reqWg.Add(1)
	s := newSession(
		id,
		envelope.EncloseReq(id, reg.filters),
		m.envoy.Send,
		m.done,
		terminate,
		withDeregister(deregister),
		withSessionInbox(si),
		withForwardEvents(reg.eventsIn),
		withForwardClosed(reg.closed),
	)
	m.sessions[id] = s
	go s.run()
}

func (m *RequestManager) start() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.done = make(chan struct{})
	for id, reg := range m.regs {
		if _, active := m.sessions[id]; !active {
			m.spawnSessionLock(id, reg)
		}
	}
}

func (m *RequestManager) stop() {
	m.mu.Lock()
	done := m.done
	m.mu.Unlock()

	if done != nil {
		close(done)
	}
	m.reqWg.Wait()

	m.mu.Lock()
	m.sessions = make(map[string]*session)
	m.inboxSubs = make(map[string]*sessionInbox)
	m.mu.Unlock()
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
		case msg, ok := <-m.inbox:
			if !ok {
				return
			}

			label, err := envelope.GetLabel(msg.Data)
			if err != nil {
				continue
			}

			switch string(label) {
			case "EVENT":
				subID, data, err := envelope.FindSubscriptionEvent(msg.Data)
				if err != nil {
					continue
				}
				m.mu.RLock()
				si, ok := m.inboxSubs[subID]
				m.mu.RUnlock()
				if !ok {
					continue
				}
				select {
				case <-m.ctx.Done():
					return
				case si.events <- inboxEvent{subID: subID, data: data, receivedAt: msg.ReceivedAt}:
				}

			case "EOSE":
				subID, err := envelope.FindEOSE(msg.Data)
				if err != nil {
					continue
				}
				m.mu.RLock()
				si, ok := m.inboxSubs[subID]
				m.mu.RUnlock()
				if !ok {
					continue
				}
				select {
				case <-m.ctx.Done():
					return
				case si.eose <- inboxEOSE{subID: subID, receivedAt: msg.ReceivedAt}:
				}

			case "CLOSED":
				subID, message, err := envelope.FindClosed(msg.Data)
				if err != nil {
					continue
				}
				m.mu.RLock()
				si, ok := m.inboxSubs[subID]
				m.mu.RUnlock()
				if !ok {
					continue
				}
				select {
				case <-m.ctx.Done():
					return
				case si.closed <- inboxClosed{subID: subID, message: message, receivedAt: msg.ReceivedAt}:
				}
			}
		}
	}
}
