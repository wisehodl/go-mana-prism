package prism

import (
	"context"
	"encoding/base32"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

// Outputs

type ReqEvent struct {
	PeerID     string
	ReceivedAt time.Time
	Event      []byte
}

type ReqMessage struct {
	PeerID     string
	ReceivedAt time.Time
	Message    string
}

// Options

const (
	defaultLabel       = "REQ"
	defaultEOSETimeout = 30 * time.Second
)

type reqConfig struct {
	id          string
	label       string
	eoseTimeout time.Duration
}

type ReqOption func(*reqConfig)

// Request Manager

type ReqManager struct {
	subs   map[string]Request
	byPeer map[string]map[string]struct{} // peerID -> subID set

	postmaster *Postmaster
	collector  *JournalCollector
	journals   chan JournalEntry // JournalCollector.Enroll

	isConnected func(peerID string) bool // Adapter.IsConnected
	poolEvents  <-chan PoolEvent         // Adapter.Subscribe
	poolInbox   <-chan InboundLetter     // Clerk.Subscribe

	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
	wg      sync.WaitGroup
	handler slog.Handler
	logger  *slog.Logger
}

type Request interface {
	order(reqTask)
	Peers() []string
	Close()
}

// Base Request

type request struct {
	id  string
	req Envelope

	tasks   chan reqTask
	closing bool
	done    chan struct{}

	buffer   chan ReqEvent
	events   chan ReqEvent
	messages chan ReqMessage

	postmaster  *Postmaster
	journals    chan<- JournalEntry
	isConnected func(peerID string) bool
	onClose     func()

	sendCtx context.Context
	wg      sync.WaitGroup
	logger  *slog.Logger
}

// Stream Request

type StreamReq struct {
	*request
	peers map[string]*streamPeer
}

type streamPeer struct {
	closeSent bool
	closed    bool
	closeOnce sync.Once
}

// Query Request

type QueryReq struct {
	*request
	peers       map[string]*queryPeer
	eoseTimeout time.Duration
}

type queryPeer struct {
	eoseTimer *time.Timer
	closeSent bool
	closed    bool
	closeOnce sync.Once
}

// Request Tasks

type reqTask interface{ reqTask() } // gates task channel

type taskRecordReqOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (taskRecordReqOutcome) reqTask() {}

type taskRecordCloseOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (taskRecordCloseOutcome) reqTask() {}

type taskReceiveEvent struct {
	peerID string
	at     time.Time
	data   Envelope
}

func (taskReceiveEvent) reqTask() {}

type taskReceiveEOSE struct {
	peerID string
	at     time.Time
}

func (taskReceiveEOSE) reqTask() {}

type taskReceiveClosed struct {
	peerID  string
	at      time.Time
	message string
}

func (taskReceiveClosed) reqTask() {}

type taskClosePeer struct{ peerID string }

func (taskClosePeer) reqTask() {}

type taskCloseReq struct{}

func (taskCloseReq) reqTask() {}

type taskHandleReconnect struct{ peerID string }

func (taskHandleReconnect) reqTask() {}

type taskHandleEOSETimeout struct{ peerID string }

func (taskHandleEOSETimeout) reqTask() {}

// ----------------------------------------------------------------------------
// Request Options
// ----------------------------------------------------------------------------

func newReqConfig(opts ...ReqOption) reqConfig {
	cfg := reqConfig{
		id:          "",
		label:       defaultLabel,
		eoseTimeout: defaultEOSETimeout,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	return cfg
}

func WithID(id string) ReqOption {
	return func(c *reqConfig) {
		c.id = id
	}
}

func WithLabel(label string) ReqOption {
	return func(c *reqConfig) {
		c.label = label
	}
}

func WithEOSETimeout(timeout time.Duration) ReqOption {
	return func(c *reqConfig) {
		c.eoseTimeout = timeout
	}
}

// ----------------------------------------------------------------------------
// Request Manager
// ----------------------------------------------------------------------------

func NewReqManager(
	ctx context.Context,
	postmaster *Postmaster,
	isConnected func(string) bool,
	poolEvents <-chan PoolEvent,
	poolInbox <-chan InboundLetter,
	collector *JournalCollector,
	handler slog.Handler,
) *ReqManager {
	return nil
}

func (m *ReqManager) OpenStream(
	filters [][]byte,
	peers []string,
	opts ...ReqOption,
) (
	id string,
	events <-chan ReqEvent,
	messages <-chan ReqMessage,
	err error,
) {
	return "", nil, nil, nil
}

func (m *ReqManager) OpenQuery(
	filters [][]byte,
	peers []string,
	opts ...ReqOption,
) (
	id string,
	events <-chan ReqEvent,
	messages <-chan ReqMessage,
	err error,
) {
	return "", nil, nil, nil
}

func (m *ReqManager) CloseReq(id string) error {
	return nil
}

func (m *ReqManager) Close() {}

func (m *ReqManager) makeOnClose(subID, peers []string) func() {
	return func() {}
}

func (m *ReqManager) routeInbox() {
	// parses envelope label and sub ID from the letter
	// looks up in sub registry
	// calls req.order()
}

func (m *ReqManager) routeEvents() {
	// reads PoolEvent
	// looks up in m.byPeer
	// calls req.order() on each matching request
}

// Helpers

func cleanPeers(peers []string) []string {
	return nil
}

var encoder = base32.StdEncoding.WithPadding(base32.NoPadding)

func generateID(prefix string) string {
	return ""
}

// ----------------------------------------------------------------------------
// Base Request
// ----------------------------------------------------------------------------

func (r *request) emit(entry JournalEntry) {
	// send into journal entry channel
	// selects on r.done and r.journals
}

func (r *request) order(task reqTask) {
	// send into task queue
	// selects on r.done and r.tasks
}

func (r *request) send(
	peerID string,
	data Envelope,
	makeOutcomeTask func(peerID string, outcome LetterOutcome) reqTask,
) error {
	return nil
}

// ----------------------------------------------------------------------------
// Stream Request
// ----------------------------------------------------------------------------

func NewStreamReq(
	ctx context.Context,
	id string,
	filters [][]byte,
	peers []string,
	postmaster *Postmaster,
	isConnected func(string) bool,
	journals chan<- JournalEntry,
	onClose func(),
	handler slog.Handler,
) *StreamReq {
	// start buffered pipe to event output
	// pipe return drives channel closures
	return nil
}

func (r *StreamReq) Peers() []string {
	return nil
}

func (r *StreamReq) Close() {}

func (r *StreamReq) sendReq(peerID string) error {
	return nil
}

func (r *StreamReq) sendClose(peerID string) error {
	return nil
}

func (r *StreamReq) run() {
	// switches on task type
}

func (r *StreamReq) applyRecordReqOutcome(task taskRecordReqOutcome) {}

func (r *StreamReq) applyRecordCloseOutcome(task taskRecordCloseOutcome) {}

func (r *StreamReq) applyReceiveEvent(task taskReceiveEvent) {}

func (r *StreamReq) applyReceiveEOSE(task taskReceiveEOSE) {}

func (r *StreamReq) applyReceiveClosed(task taskReceiveClosed) {}

func (r *StreamReq) applyClosePeer(task taskClosePeer) {}

func (r *StreamReq) applyCloseReq(task taskCloseReq) {}

func (r *StreamReq) applyHandleReconnect(task taskHandleReconnect) {}

// ----------------------------------------------------------------------------
// Query Request
// ----------------------------------------------------------------------------

func NewQueryReq(
	ctx context.Context,
	id string,
	filters [][]byte,
	peers []string,
	postmaster *Postmaster,
	isConnected func(string) bool,
	journals chan<- JournalEntry,
	eoseTimeout time.Duration,
	onClose func(),
	handler slog.Handler,
) *QueryReq {
	// start buffered pipe to event output
	// pipe return drives channel closures
	return nil
}

func (r *QueryReq) Peers() []string {
	return nil
}

func (r *QueryReq) Close() {}

func (r *QueryReq) sendReq(peerID string) error {
	return nil
}

func (r *QueryReq) sendClose(peerID string) error {
	return nil
}

func (r *QueryReq) run() {
	// switches on task type
}

func (r *QueryReq) applyRecordReqOutcome(task taskRecordReqOutcome) {}

func (r *QueryReq) applyRecordCloseOutcome(task taskRecordCloseOutcome) {}

func (r *QueryReq) applyReceiveEvent(task taskReceiveEvent) {}

func (r *QueryReq) applyReceiveEOSE(task taskReceiveEOSE) {}

func (r *QueryReq) applyReceiveClosed(task taskReceiveClosed) {}

func (r *QueryReq) applyClosePeer(task taskClosePeer) {}

func (r *QueryReq) applyCloseReq(task taskCloseReq) {}

func (r *QueryReq) applyHandleEOSETimeout(task taskHandleEOSETimeout) {}

// ----------------------------------------------------------------------------
// Request Tasks
// ----------------------------------------------------------------------------

func newRecordReqOutcome(peerID string, outcome LetterOutcome) taskRecordReqOutcome {
	return taskRecordReqOutcome{peerID: peerID, outcome: outcome}
}

func newRecordCloseOutcome(peerID string, outcome LetterOutcome) taskRecordCloseOutcome {
	return taskRecordCloseOutcome{peerID: peerID, outcome: outcome}
}

func newReceiveEvent(peerID string, at time.Time, data Envelope) taskReceiveEvent {
	return taskReceiveEvent{peerID: peerID, at: at, data: data}
}

func newReceiveEOSE(peerID string, at time.Time) taskReceiveEOSE {
	return taskReceiveEOSE{peerID: peerID, at: at}
}

func newReceiveClosed(peerID string, at time.Time, message string) taskReceiveClosed {
	return taskReceiveClosed{peerID: peerID, at: at, message: message}
}

func newClosePeer(peerID string) taskClosePeer {
	return taskClosePeer{peerID: peerID}
}

func newCloseReq() taskCloseReq {
	return taskCloseReq{}
}

func newHandleReconnect(peerID string) taskHandleReconnect {
	return taskHandleReconnect{peerID: peerID}
}

func newHandleEOSETimeout(peerID string) taskHandleEOSETimeout {
	return taskHandleEOSETimeout{peerID: peerID}
}
