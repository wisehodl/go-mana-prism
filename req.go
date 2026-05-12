package prism

import (
	"context"
	"encoding/base32"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"log/slog"
	"sync"
	"time"
)

var (
	_ fmt.Formatter
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

// Outputs

type ReqEvent struct {
	PeerID     string
	ReceivedAt time.Time
	Data       []byte
}

type ReqMessage struct {
	PeerID     string
	ReceivedAt time.Time
	Data       string
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
	journals    chan JournalEntry
	isConnected func(peerID string) bool
	onClose     func()

	ctx    context.Context
	wg     sync.WaitGroup
	peerWg sync.WaitGroup
	logger *slog.Logger
}

// Stream Request

type StreamReq struct {
	*request
	peers map[string]*streamPeer
}

type streamPeer struct {
	reqSent   bool
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
	reqSent   bool
	eoseTimer *time.Timer
	closeSent bool
	closed    bool
	closeOnce sync.Once
}

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

func (m *ReqManager) makeOnClose(subID string, peers []string) func() {
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

func (r *request) runReturnEvents() {
	defer r.wg.Done()
	defer close(r.events)
	defer close(r.messages)
	bufferedPipe(r.buffer, r.events)
}

func (r *request) dispatchEvent(task taskEvent) {
	select {
	case <-r.done:
	case r.buffer <- ReqEvent{
		PeerID: task.peerID, ReceivedAt: task.at, Data: task.data}:
	}
}

func (r *request) emit(entry JournalEntry) {
	select {
	case <-r.done:
	case r.journals <- entry:
	}
}

func (r *request) order(task reqTask) {
	select {
	case <-r.done:
	case r.tasks <- task:
	}
}

func (r *request) Close() {
	r.order(newCloseReq())
	r.wg.Wait()
}

func (r *request) terminate() {
	defer r.wg.Done()
	r.peerWg.Wait()
	close(r.done)
	close(r.buffer)
	if r.journals != nil {
		close(r.journals)
	}
	r.onClose()
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
	collector *JournalCollector,
	onClose func(),
	handler slog.Handler,
) *StreamReq {
	ctx = component.MustExtend(ctx, "stream")

	r := &StreamReq{
		request: &request{
			id:  id,
			req: envelope.EncloseReq(id, filters),

			tasks: make(chan reqTask, len(peers)*16),
			done:  make(chan struct{}),

			buffer:   make(chan ReqEvent, len(peers)*16),
			events:   make(chan ReqEvent),
			messages: make(chan ReqMessage, len(peers)),

			postmaster:  postmaster,
			isConnected: isConnected,
			onClose:     onClose,

			ctx: ctx,
		},
		peers: make(map[string]*streamPeer),
	}

	if collector != nil {
		r.journals = make(chan JournalEntry, len(peers)*16)
		collector.Enroll(r.journals)
	}

	if handler != nil {
		c := component.FromContext(ctx)
		r.logger = slog.New(handler).With(slog.Any("component", c))
	}

	for _, peerID := range peers {
		r.peers[peerID] = &streamPeer{}
		r.peerWg.Add(1)
	}

	r.wg.Add(2)
	go r.run()
	go r.runReturnEvents()

	// send initial REQs
	for id := range r.peers {
		if r.isConnected(id) {
			r.sendReq(id)
		}
	}

	return r
}

func (r *StreamReq) run() {
	defer r.wg.Done()

	for {
		select {
		case <-r.done:
			return
		case t := <-r.tasks:
			r.dispatch(t)
		}
	}
}

func (r *StreamReq) Peers() []string {
	peers := make([]string, 0, len(r.peers))
	for p := range r.peers {
		peers = append(peers, p)
	}
	return peers
}

func (r *StreamReq) sendReq(peerID string) {
	_, ok := r.peers[peerID]
	if !ok {
		return
	}

	id, _ := r.postmaster.Send(r.ctx, peerID, r.req,
		func(o LetterOutcome) { r.order(newReqOutcomeTask(peerID, o)) })

	c := component.FromContext(r.ctx)
	r.emit(NewReqQueuedJournal(peerID, c, ReqQueuedData{
		SubID: r.id, LetterID: id, QueuedAt: time.Now(),
	}))
}

func (r *StreamReq) sendClose(peerID string) {
	peer, ok := r.peers[peerID]
	if !ok || peer.closeSent {
		return
	}

	if !peer.reqSent {
		r.closePeer(peerID)
		return
	}

	id, _ := r.postmaster.Send(r.ctx, peerID, envelope.EncloseClose(r.id),
		func(o LetterOutcome) { r.order(newCloseOutcomeTask(peerID, o)) })

	peer.closeSent = true
	c := component.FromContext(r.ctx)
	r.emit(NewCloseQueuedJournal(peerID, c, CloseQueuedData{
		SubID: r.id, LetterID: id, QueuedAt: time.Now(),
	}))
}

func (r *StreamReq) closePeer(peerID string) {
	peer, ok := r.peers[peerID]
	if !ok {
		return
	}

	peer.closeOnce.Do(func() {
		r.peerWg.Done()
		peer.closed = true
	})
}

func (r *StreamReq) dispatch(task reqTask) {
	switch t := task.(type) {
	case taskReqOutcome:
		r.dispatchReqOutcome(t)

	case taskCloseOutcome:
		r.dispatchCloseOutcome(t)

	case taskEvent:
		r.dispatchEvent(t)

	case taskEOSE:
		r.dispatchEOSE(t)

	case taskClosed:
		r.dispatchClosed(t)

	case taskClosePeer:
		r.dispatchClosePeer(t)

	case taskCloseReq:
		r.dispatchCloseReq(t)

	case taskHandleReconnect:
		r.dispatchHandleReconnect(t)
	}
}

func (r *StreamReq) dispatchReqOutcome(task taskReqOutcome) {
	peer := r.peers[task.peerID]
	if task.outcome.Kind == OutcomeSent {
		peer.reqSent = true
	}

	c := component.FromContext(r.ctx)
	r.emit(NewReqSendOutcomeJournal(task.peerID, c, ReqSendOutcomeData{
		SubID:      r.id,
		LetterID:   task.outcome.LetterID,
		Outcome:    task.outcome.Kind,
		SentAt:     task.outcome.SentAt,
		MissedAt:   task.outcome.MissedAt,
		RetryCount: task.outcome.Retries,
	}))
}

func (r *StreamReq) dispatchCloseOutcome(task taskCloseOutcome) {
	r.closePeer(task.peerID)

	c := component.FromContext(r.ctx)
	r.emit(NewCloseSendOutcomeJournal(task.peerID, c, CloseSendOutcomeData{
		SubID:      r.id,
		LetterID:   task.outcome.LetterID,
		Outcome:    task.outcome.Kind,
		SentAt:     task.outcome.SentAt,
		MissedAt:   task.outcome.MissedAt,
		RetryCount: task.outcome.Retries,
	}))
}

func (r *StreamReq) dispatchEOSE(task taskEOSE) {
	c := component.FromContext(r.ctx)
	r.emit(NewReceivedEOSEJournal(task.peerID, c, ReceivedEOSEData{
		SubID: r.id, At: task.at,
	}))
}

func (r *StreamReq) dispatchClosed(task taskClosed) {
	c := component.FromContext(r.ctx)
	r.emit(NewReceivedClosedJournal(task.peerID, c, ReceivedClosedData{
		SubID: r.id, At: task.at, Message: task.message,
	}))

	peer := r.peers[task.peerID]
	if peer.closed {
		return
	}

	select {
	case <-r.done:
	case r.messages <- ReqMessage{
		PeerID:     task.peerID,
		ReceivedAt: task.at,
		Data:       task.message,
	}:
	}

	r.closePeer(task.peerID)
}

func (r *StreamReq) dispatchClosePeer(task taskClosePeer) {
	r.closePeer(task.peerID)
}

func (r *StreamReq) dispatchCloseReq(task taskCloseReq) {
	if r.closing {
		return
	}

	r.closing = true

	for id, peer := range r.peers {
		if !peer.closed {
			if !r.isConnected(id) {
				r.closePeer(id)
			} else {
				r.sendClose(id)
			}
		}
	}

	r.wg.Add(1)
	go r.terminate()
}

func (r *StreamReq) dispatchHandleReconnect(task taskHandleReconnect) {
	peer, ok := r.peers[task.peerID]
	if !ok || peer.closed || r.closing || peer.closeSent {
		return
	}
	r.sendReq(task.peerID)
}

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
	peers := make([]string, 0, len(r.peers))
	for p := range r.peers {
		peers = append(peers, p)
	}
	return peers
}

func (r *QueryReq) sendReq(peerID string) error {
	return nil
}

func (r *QueryReq) sendClose(peerID string) error {
	return nil
}

func (r *QueryReq) run() {
	defer r.wg.Done()

	for {
		select {
		case <-r.done:
			return
		case t := <-r.tasks:
			r.dispatch(t)
		}
	}
}

func (r *QueryReq) dispatch(task reqTask) {
	switch t := task.(type) {
	case taskReqOutcome:
		r.dispatchReqOutcome(t)

	case taskCloseOutcome:
		r.dispatchCloseOutcome(t)

	case taskEvent:
		r.dispatchEvent(t)

	case taskEOSE:
		r.dispatchEOSE(t)

	case taskClosed:
		r.dispatchClosed(t)

	case taskClosePeer:
		r.dispatchClosePeer(t)

	case taskCloseReq:
		r.dispatchCloseReq(t)

	case taskMissedEOSE:
		r.dispatchMissedEOSE(t)
	}
}

func (r *QueryReq) dispatchReqOutcome(task taskReqOutcome) {}

func (r *QueryReq) dispatchCloseOutcome(task taskCloseOutcome) {}

func (r *QueryReq) dispatchEOSE(task taskEOSE) {}

func (r *QueryReq) dispatchClosed(task taskClosed) {}

func (r *QueryReq) dispatchClosePeer(task taskClosePeer) {}

func (r *QueryReq) dispatchCloseReq(task taskCloseReq) {}

func (r *QueryReq) dispatchMissedEOSE(task taskMissedEOSE) {}

// ----------------------------------------------------------------------------
// Request Tasks
// ----------------------------------------------------------------------------

// Types

type reqTask interface{ reqTask() } // gates task channel

type taskReqOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (taskReqOutcome) reqTask() {}

type taskCloseOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (taskCloseOutcome) reqTask() {}

type taskEvent struct {
	peerID string
	at     time.Time
	data   Envelope
}

func (taskEvent) reqTask() {}

type taskEOSE struct {
	peerID string
	at     time.Time
}

func (taskEOSE) reqTask() {}

type taskClosed struct {
	peerID  string
	at      time.Time
	message string
}

func (taskClosed) reqTask() {}

type taskClosePeer struct{ peerID string }

func (taskClosePeer) reqTask() {}

type taskCloseReq struct{}

func (taskCloseReq) reqTask() {}

type taskHandleReconnect struct{ peerID string }

func (taskHandleReconnect) reqTask() {}

type taskMissedEOSE struct{ peerID string }

func (taskMissedEOSE) reqTask() {}

// Constructors

func newReqOutcomeTask(peerID string, outcome LetterOutcome) taskReqOutcome {
	return taskReqOutcome{peerID: peerID, outcome: outcome}
}

func newCloseOutcomeTask(peerID string, outcome LetterOutcome) taskCloseOutcome {
	return taskCloseOutcome{peerID: peerID, outcome: outcome}
}

func newEventTask(peerID string, at time.Time, data Envelope) taskEvent {
	return taskEvent{peerID: peerID, at: at, data: data}
}

func newEOSETask(peerID string, at time.Time) taskEOSE {
	return taskEOSE{peerID: peerID, at: at}
}

func newClosedTask(peerID string, at time.Time, message string) taskClosed {
	return taskClosed{peerID: peerID, at: at, message: message}
}

func newClosePeerTask(peerID string) taskClosePeer {
	return taskClosePeer{peerID: peerID}
}

func newCloseReq() taskCloseReq {
	return taskCloseReq{}
}

func newHandleReconnect(peerID string) taskHandleReconnect {
	return taskHandleReconnect{peerID: peerID}
}

func newMissedEOSETask(peerID string) taskMissedEOSE {
	return taskMissedEOSE{peerID: peerID}
}
