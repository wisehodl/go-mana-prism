package prism

import (
	"context"
	"crypto/rand"
	"encoding/base32"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
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
	id      string
	filters [][]byte
	buffer  chan ReqEvent
	events  chan ReqEvent
	closed  chan ReqClosed
	once    sync.Once
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
	eose   chan<- struct{}
	closed chan<- struct{}
}

type terminateReason int

const (
	termSendFailed terminateReason = iota
	termCloseSent
	termReceivedClosed
	termExternal
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
	// start inbox router

	return m
}

func (m *RequestManager) Stream(
	filters [][]byte,
) (string, <-chan ReqEvent, <-chan ReqClosed) {
	// generate id
	// create channels
	// register request
	// spawn session if connected
	return "", nil, nil
}

func (m *RequestManager) Query(
	filters [][]byte,
	timeout time.Duration,
) (events []ReqEvent, closed *ReqClosed) {
	// return if disconnected
	// generate id
	// create channels
	// spawn session
	// collect events
	return
}

func (m *RequestManager) Close() {
	m.cancel()
	m.wg.Wait()
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

	// unpack/route inbox message
	// events forward directly to request event buffer
	// eose goes to session
	// closed goes both to session and request
	// uses read lock for map lookups
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
			s.terminate(termExternal)
			return
		case <-s.ctx.Done():
			s.terminate(termExternal)
			return
		case <-s.eose:
		}
	}
}

func (s *session) Close() {
	s.cancel()
}
