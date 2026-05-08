package prism

import (
	"context"
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
	subs   map[string]*Request
	byPeer map[string]map[string]struct{} // peerID -> subID set

	postmaster *Postmaster
	collector  *JournalCollector
	journals   chan JournalEntry // JournalCollector.Enroll

	isConnected func(peerID string) bool // Adapter.IsConnected
	poolEvents  <-chan PoolEvent         // Adapter.Subscribe
	inbox       <-chan InboundLetter     // Clerk.Subscribe

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

type Request interface {
	command(reqCommand)
	Peers() []string
	Close()
}

// Base Request

type request struct {
	id  string
	req Envelope

	cmds    chan reqCommand
	closing bool
	done    chan struct{}

	events   chan ReqEvent
	messages chan ReqMessage

	postmaster  *Postmaster
	journals    chan<- JournalEntry
	isConnected func() bool
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

// Commands

type reqCommand interface{ reqCommand() } // gates command channel

type cmdRecordReqOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (cmdRecordReqOutcome) reqCommand() {}

type cmdRecordCloseOutcome struct {
	peerID  string
	outcome LetterOutcome
}

func (cmdRecordCloseOutcome) reqCommand() {}

type cmdReceiveEvent struct {
	peerID string
	at     time.Time
	data   Envelope
}

func (cmdReceiveEvent) reqCommand() {}

type cmdReceiveEOSE struct {
	peerID string
	at     time.Time
}

func (cmdReceiveEOSE) reqCommand() {}

type cmdReceiveClosed struct {
	peerID  string
	at      time.Time
	message string
}

func (cmdReceiveClosed) reqCommand() {}

type cmdClosePeer struct{ peerID string }

func (cmdClosePeer) reqCommand() {}

type cmdCloseReq struct{}

func (cmdCloseReq) reqCommand() {}

type cmdHandleReconnect struct{ peerID string }

func (cmdHandleReconnect) reqCommand() {}

type cmdHandleEOSETimeout struct{ peerID string }

func (cmdHandleEOSETimeout) reqCommand() {}

// ----------------------------------------------------------------------------
// Request Options
// ----------------------------------------------------------------------------

func newReqConfig(opts ...ReqOption) reqConfig {
	return reqConfig{}
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

func NewReqManager() *ReqManager {
	return nil
}

func (m *ReqManager) OpenStream() {}

func (m *ReqManager) OpenQuery() {}

func (m *ReqManager) CloseReq() {}

func (m *ReqManager) Close() {}

func (m *ReqManager) makeOnClose() {}

func (m *ReqManager) routeInbox() {}

func (m *ReqManager) routeEvents() {}

// Helpers

func cleanPeers() {}

var encoder struct{}

func generateID() {}

// ----------------------------------------------------------------------------
// Base Request
// ----------------------------------------------------------------------------

func (r *request) emit() {}

func (r *request) command() {}

func (r *request) send() {}

// ----------------------------------------------------------------------------
// Stream Request
// ----------------------------------------------------------------------------

func NewStreamReq() *StreamReq {
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
	// buffers command queue internally
	// switches on command type
}

func (r *StreamReq) applyRecordReqOutcome(cmd cmdRecordReqOutcome) {}

func (r *StreamReq) applyRecordCloseOutcome(cmd cmdRecordCloseOutcome) {}

func (r *StreamReq) applyReceiveEvent(cmd cmdReceiveEvent) {}

func (r *StreamReq) applyReceiveEOSE(cmd cmdReceiveEOSE) {}

func (r *StreamReq) applyReceiveClosed(cmd cmdReceiveClosed) {}

func (r *StreamReq) applyClosePeer(cmd cmdClosePeer) {}

func (r *StreamReq) applyCloseReq(cmd cmdCloseReq) {}

func (r *StreamReq) applyHandleReconnect(cmd cmdHandleReconnect) {}

// ----------------------------------------------------------------------------
// Query Request
// ----------------------------------------------------------------------------

func NewQueryReq() *QueryReq {
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
	// buffers command queue internally
	// switches on command type
}

func (r *QueryReq) applyRecordReqOutcome(cmd cmdRecordReqOutcome) {}

func (r *QueryReq) applyRecordCloseOutcome(cmd cmdRecordCloseOutcome) {}

func (r *QueryReq) applyReceiveEvent(cmd cmdReceiveEvent) {}

func (r *QueryReq) applyReceiveEOSE(cmd cmdReceiveEOSE) {}

func (r *QueryReq) applyReceiveClosed(cmd cmdReceiveClosed) {}

func (r *QueryReq) applyClosePeer(cmd cmdClosePeer) {}

func (r *QueryReq) applyCloseReq(cmd cmdCloseReq) {}

func (r *QueryReq) applyHandleEOSETimeout(cmd cmdHandleEOSETimeout) {}

// ----------------------------------------------------------------------------
// Commands
// ----------------------------------------------------------------------------

func newRecordReqOutcome(peerID string, outcome LetterOutcome) cmdRecordReqOutcome {
	return cmdRecordReqOutcome{peerID: peerID, outcome: outcome}
}

func newRecordCloseOutcome(peerID string, outcome LetterOutcome) cmdRecordCloseOutcome {
	return cmdRecordCloseOutcome{peerID: peerID, outcome: outcome}
}

func newReceiveEvent(peerID string, at time.Time, data Envelope) cmdReceiveEvent {
	return cmdReceiveEvent{peerID: peerID, at: at, data: data}
}

func newReceiveEOSE(peerID string, at time.Time) cmdReceiveEOSE {
	return cmdReceiveEOSE{peerID: peerID, at: at}
}

func newReceiveClosed(peerID string, at time.Time, message string) cmdReceiveClosed {
	return cmdReceiveClosed{peerID: peerID, at: at, message: message}
}

func newClosePeer(peerID string) cmdClosePeer {
	return cmdClosePeer{peerID: peerID}
}

func newCloseReq() cmdCloseReq {
	return cmdCloseReq{}
}

func newHandleReconnect(peerID string) cmdHandleReconnect {
	return cmdHandleReconnect{peerID: peerID}
}

func newHandleEOSETimeout(peerID string) cmdHandleEOSETimeout {
	return cmdHandleEOSETimeout{peerID: peerID}
}
