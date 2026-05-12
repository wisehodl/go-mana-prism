package prism

import (
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

// JournalCollector

type JournalCollector struct {
	out     chan JournalEntry
	buffer  chan JournalEntry
	mu      sync.Mutex
	wg      sync.WaitGroup
	closing bool
}

// JournalEntry

type JournalEntry interface {
	PeerID() string
	SealedAt() time.Time
	Component() component.Component
}

type entry struct {
	peerID    string
	sealedAt  time.Time
	component component.Component
}

func (e *entry) PeerID() string                 { return e.peerID }
func (e *entry) SealedAt() time.Time            { return e.sealedAt }
func (e *entry) Component() component.Component { return e.component }

// ----------------------------------------------------------------------------
// Journal Collector
// ----------------------------------------------------------------------------

func NewJournalCollector() *JournalCollector {
	c := &JournalCollector{
		out:    make(chan JournalEntry),
		buffer: make(chan JournalEntry, 1024),
	}

	go func() {
		bufferedPipe(c.buffer, c.out)
		close(c.out)
	}()

	return c
}

func (c *JournalCollector) Enroll(ch <-chan JournalEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closing {
		return fmt.Errorf("journal collector is closing")
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		for e := range ch {
			c.buffer <- e
		}
	}()

	return nil
}

func (c *JournalCollector) Close() {
	c.mu.Lock()
	if c.closing {
		c.mu.Unlock()
		return
	}
	c.closing = true
	c.mu.Unlock()

	c.wg.Wait()
	close(c.buffer)
}

func (c *JournalCollector) Out() <-chan JournalEntry { return c.out }

// ----------------------------------------------------------------------------
// Journal Entries
// ----------------------------------------------------------------------------

func newEntry(peerID string, component component.Component) *entry {
	return &entry{
		peerID:    peerID,
		component: component,
		sealedAt:  time.Now(),
	}
}

// PeerAdded

type PeerAddedJournal struct {
	*entry
	Data PeerAddedData
}

type PeerAddedData struct {
	At time.Time
}

func NewPeerAddedJournal(
	peerID string, component component.Component, data PeerAddedData,
) PeerAddedJournal {
	return PeerAddedJournal{entry: newEntry(peerID, component), Data: data}
}

// PeerRemoved

type PeerRemovedJournal struct {
	*entry
	Data PeerRemovedData
}

type PeerRemovedData struct {
	At time.Time
}

func NewPeerRemovedJournal(
	peerID string, component component.Component, data PeerRemovedData,
) PeerRemovedJournal {
	return PeerRemovedJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data PeerConnectedData,
) PeerConnectedJournal {
	return PeerConnectedJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data PeerDisconnectedData,
) PeerDisconnectedJournal {
	return PeerDisconnectedJournal{entry: newEntry(peerID, component), Data: data}
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
}

func NewReqQueuedJournal(
	peerID string, component component.Component, data ReqQueuedData,
) ReqQueuedJournal {
	return ReqQueuedJournal{entry: newEntry(peerID, component), Data: data}
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
}

func NewCloseQueuedJournal(
	peerID string, component component.Component, data CloseQueuedData,
) CloseQueuedJournal {
	return CloseQueuedJournal{entry: newEntry(peerID, component), Data: data}
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
}

func NewReqSendOutcomeJournal(
	peerID string, component component.Component, data ReqSendOutcomeData,
) ReqSendOutcomeJournal {
	return ReqSendOutcomeJournal{entry: newEntry(peerID, component), Data: data}
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
}

func NewCloseSendOutcomeJournal(
	peerID string, component component.Component, data CloseSendOutcomeData,
) CloseSendOutcomeJournal {
	return CloseSendOutcomeJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data ReceivedEOSEData,
) ReceivedEOSEJournal {
	return ReceivedEOSEJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data MissedEOSEData,
) MissedEOSEJournal {
	return MissedEOSEJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data ReceivedClosedData,
) ReceivedClosedJournal {
	return ReceivedClosedJournal{entry: newEntry(peerID, component), Data: data}
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
	peerID string, component component.Component, data ReqClosedData,
) ReqClosedJournal {
	return ReqClosedJournal{entry: newEntry(peerID, component), Data: data}
}
