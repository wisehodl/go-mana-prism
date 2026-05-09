package prism

import (
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type JournalAuthor string

const (
	AuthorEmbassy    JournalAuthor = "prism.embassy"
	AuthorReqManager JournalAuthor = "prism.req_manager"
	AuthorStreamReq  JournalAuthor = "prism.stream_req"
	AuthorQueryReq   JournalAuthor = "prism.query_req"
)

// JournalCollector

type JournalCollector struct {
	entries chan JournalEntry
	mu      sync.Mutex
	wg      sync.WaitGroup
	closing bool
}

// JournalEntry

type JournalEntry interface {
	PeerID() string
	SealedAt() time.Time
	Author() JournalAuthor
}

type entry struct {
	peerID   string
	sealedAt time.Time
	author   JournalAuthor
}

func (e *entry) PeerID() string        { return e.peerID }
func (e *entry) SealedAt() time.Time   { return e.sealedAt }
func (e *entry) Author() JournalAuthor { return e.author }

// ----------------------------------------------------------------------------
// Journal Collector
// ----------------------------------------------------------------------------

func NewJournalCollector() *JournalCollector {
	return nil
}

func (c *JournalCollector) Enroll(ch <-chan JournalEntry) {}

func (c *JournalCollector) Close() {}

func (c *JournalCollector) Entries() {}

// ----------------------------------------------------------------------------
// Journal Entries
// ----------------------------------------------------------------------------

func newEntry(peerID string, author JournalAuthor) *entry {
	return &entry{
		peerID:   peerID,
		author:   author,
		sealedAt: time.Now(),
	}
}

// PeerConnected

type PeerConnectedJournal struct {
	*entry
	Data PeerConnectedData
}

type PeerConnectedData struct {
	At time.Time
}

func NewPeerConnectedJournal(
	peerID string, author JournalAuthor, data PeerConnectedData,
) PeerConnectedJournal {
	return PeerConnectedJournal{entry: newEntry(peerID, author), Data: data}
}

// PeerDisconnected

type PeerDisconnectedJournal struct {
	*entry
	Data PeerDisconnectedData
}

type PeerDisconnectedData struct {
	At time.Time
}

func NewPeerDisconnectedJournal(
	peerID string, author JournalAuthor, data PeerDisconnectedData,
) PeerDisconnectedJournal {
	return PeerDisconnectedJournal{entry: newEntry(peerID, author), Data: data}
}

// ReqQueued

type ReqQueuedJournal struct {
	*entry
	Data ReqQueuedData
}

type ReqQueuedData struct {
	SubID    string
	LetterID uint64
	QueuedAt time.Time
	Err      error
}

func NewReqQueuedJournal(
	peerID string, author JournalAuthor, data ReqQueuedData,
) ReqQueuedJournal {
	return ReqQueuedJournal{entry: newEntry(peerID, author), Data: data}
}

// CloseQueued

type CloseQueuedJournal struct {
	*entry
	Data CloseQueuedData
}

type CloseQueuedData struct {
	SubID    string
	LetterID uint64
	QueuedAt time.Time
	Err      error
}

func NewCloseQueuedJournal(
	peerID string, author JournalAuthor, data CloseQueuedData,
) CloseQueuedJournal {
	return CloseQueuedJournal{entry: newEntry(peerID, author), Data: data}
}

// ReqSendOutcome

type ReqSendOutcomeJournal struct {
	*entry
	Data ReqSendOutcomeData
}

type ReqSendOutcomeData struct {
	SubID      string
	LetterID   uint64
	Outcome    LetterOutcomeKind
	SentAt     time.Time
	MissedAt   time.Time
	RetryCount int
	Err        error
}

func NewReqSendOutcomeJournal(
	peerID string, author JournalAuthor, data ReqSendOutcomeData,
) ReqSendOutcomeJournal {
	return ReqSendOutcomeJournal{entry: newEntry(peerID, author), Data: data}
}

// CloseSendOutcome

type CloseSendOutcomeJournal struct {
	*entry
	Data CloseSendOutcomeData
}

type CloseSendOutcomeData struct {
	SubID      string
	LetterID   uint64
	Outcome    LetterOutcomeKind
	SentAt     time.Time
	MissedAt   time.Time
	RetryCount int
	Err        error
}

func NewCloseSendOutcomeJournal(
	peerID string, author JournalAuthor, data CloseSendOutcomeData,
) CloseSendOutcomeJournal {
	return CloseSendOutcomeJournal{entry: newEntry(peerID, author), Data: data}
}

// ReceivedEOSE

type ReceivedEOSEJournal struct {
	*entry
	Data ReceivedEOSEData
}

type ReceivedEOSEData struct {
	SubID string
	At    time.Time
}

func NewReceivedEOSEJournal(
	peerID string, author JournalAuthor, data ReceivedEOSEData,
) ReceivedEOSEJournal {
	return ReceivedEOSEJournal{entry: newEntry(peerID, author), Data: data}
}

// MissedEOSE

type MissedEOSEJournal struct {
	*entry
	Data MissedEOSEData
}

type MissedEOSEData struct {
	SubID string
	At    time.Time
}

func NewMissedEOSEJournal(
	peerID string, author JournalAuthor, data MissedEOSEData,
) MissedEOSEJournal {
	return MissedEOSEJournal{entry: newEntry(peerID, author), Data: data}
}

// ReceivedClosed

type ReceivedClosedJournal struct {
	*entry
	Data ReceivedClosedData
}

type ReceivedClosedData struct {
	SubID   string
	At      time.Time
	Message string
}

func NewReceivedClosedJournal(
	peerID string, author JournalAuthor, data ReceivedClosedData,
) ReceivedClosedJournal {
	return ReceivedClosedJournal{entry: newEntry(peerID, author), Data: data}
}

// ReqClosed

type ReqClosedJournal struct {
	*entry
	Data ReqClosedData
}

type ReqClosedData struct {
	SubID string
	At    time.Time
}

func NewReqClosedJournal(
	peerID string, author JournalAuthor, data ReqClosedData,
) ReqClosedJournal {
	return ReqClosedJournal{entry: newEntry(peerID, author), Data: data}
}
