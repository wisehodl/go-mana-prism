package prism

import (
	"context"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"reflect"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// TODO: remove
var (
	_ context.Context
	_ assert.Assertions
	_ testing.T
	_ time.Time
	_ fmt.Formatter
)

// Helpers

type reqTestHarness struct {
	ctx         context.Context
	pm          *Postmaster
	events      chan PoolEvent
	sent        map[string][]string
	sentMu      *sync.RWMutex
	isConnected func(string) bool
	collector   *JournalCollector
	journals    <-chan JournalEntry
	closed      atomic.Bool
}

func setupReqHarness(t *testing.T, peers []string) reqTestHarness {
	ctx := component.MustNew(context.Background(), "prism", "test")
	pm, poolEvents, sent, sentMu, isConnected := mockReqPostmaster(t, ctx, peers)
	collector := NewJournalCollector()
	journals := collector.Out()
	return reqTestHarness{
		ctx:         ctx,
		pm:          pm,
		events:      poolEvents,
		sent:        sent,
		sentMu:      sentMu,
		isConnected: isConnected,
		collector:   collector,
		journals:    journals,
	}
}

func mockReqPostmaster(
	t *testing.T,
	ctx context.Context,
	peers []string,
) (
	pm *Postmaster,
	poolEvents chan PoolEvent,
	sent map[string][]string,
	sentMu *sync.RWMutex,
	isConnected func(id string) bool,
) {
	t.Helper()

	poolHasPeer := func(id string) (string, bool) {
		if ok := slices.Contains(peers, id); ok {
			return id, true
		}
		return "", false
	}

	poolEvents = make(chan PoolEvent, 4)
	pmEvents := make(chan PoolEvent, 4)

	connected := make(map[string]bool)
	connMu := sync.RWMutex{}
	isConnected = func(id string) bool {
		connMu.RLock()
		defer connMu.RUnlock()
		return connected[id]
	}

	go func() {
		for ev := range poolEvents {
			connMu.Lock()
			switch ev.Kind {
			case EventConnected:
				connected[ev.ID] = true
			case EventDisconnected:
				connected[ev.ID] = false
			}
			connMu.Unlock()
			pmEvents <- ev
		}
	}()

	sent = make(map[string][]string)
	sentMu = &sync.RWMutex{}
	poolSendFunc := func(id string, data Envelope) error {
		sentMu.Lock()
		defer sentMu.Unlock()
		sent[id] = append(sent[id], string(data))
		return nil
	}

	pm = NewPostmaster(ctx, poolHasPeer, pmEvents, poolSendFunc, nil)

	for _, id := range peers {
		poolEvents <- PoolEvent{ID: id, Kind: EventAdded, At: time.Now()}
		connected[id] = false
	}

	Eventually(t, func() bool { return len(pm.Peers()) == len(peers) },
		"should add peers")

	return
}

func expectSentMessage(t *testing.T,
	sent map[string][]string,
	mu *sync.RWMutex,
	peerID string,
	msg []byte,
	index int,
) {
	t.Helper()

	Eventually(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()
		if len(sent[peerID]) <= index {
			return false
		}
		return sent[peerID][index] == string(msg)
	}, fmt.Sprintf("expected message to be sent to %q: %s", peerID, string(msg)))
}

func neverSentMessage(t *testing.T,
	sent map[string][]string,
	mu *sync.RWMutex,
	peerID string,
	msg []byte,
	index int,
) {
	t.Helper()

	Never(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()
		if len(sent[peerID]) <= index {
			return false
		}
		return sent[peerID][index] == string(msg)
	}, fmt.Sprintf("unexpected message sent to %q: %s", peerID, string(msg)))
}

func expectJournalEntry(t *testing.T,
	journals <-chan JournalEntry,
	expected reflect.Type,
) {
	t.Helper()

	Eventually(t, func() bool {
		select {
		default:
			return false
		case entry := <-journals:
			got := reflect.TypeOf(entry)
			return expected == got
		}
	}, fmt.Sprintf("expected journal entry: %s", expected))
}

// Tests

func TestStreamReq_InitialReq(t *testing.T) {
	t.Run("sends req to one peer", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// connect to peer
		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqQueuedJournal{}))
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))
		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		// close req
		req.Close()

		expectJournalEntry(t, h.journals, reflect.TypeOf(CloseQueuedJournal{}))
		expectJournalEntry(t, h.journals, reflect.TypeOf(CloseSendOutcomeJournal{}))
		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 1)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("doesn't send to disconnected peer", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		// close req
		req.Close()

		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 0)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("sends req to multiple peers", func(t *testing.T) {
		peers := []string{"peer1", "peer2"}
		h := setupReqHarness(t, peers)

		// connect to peers
		h.events <- PoolEvent{ID: "peer1", Kind: EventConnected, At: time.Now()}
		h.events <- PoolEvent{ID: "peer2", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer1") },
			"expected peer 1 to connect")
		Eventually(t, func() bool { return h.isConnected("peer2") },
			"expected peer 2 to connect")

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		expectSentMessage(t, h.sent, h.sentMu, "peer1", expectedReq, 0)
		expectSentMessage(t, h.sent, h.sentMu, "peer2", expectedReq, 0)

		// expect two req outcomes
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))

		// close req
		req.Close()

		expectSentMessage(t, h.sent, h.sentMu, "peer1", expectedClose, 1)
		expectSentMessage(t, h.sent, h.sentMu, "peer2", expectedClose, 1)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})
}

func TestStreamReq_EventForwarding(t *testing.T) {
	t.Run("events are forwarded", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		// simulate receive event
		req.order(newEventTask("peer", time.Now(), []byte("event")))

		// receive event
		var event ReqEvent
		Eventually(t, func() bool {
			select {
			default:
				return false
			case event = <-req.events:
				return true
			}
		}, "expected event")

		assert.Equal(t, "peer", event.PeerID)
		assert.False(t, event.ReceivedAt.IsZero())
		assert.Equal(t, []byte("event"), event.Data)
	})

	t.Run("events channel closes on close", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		// close req
		req.Close()

		Eventually(t, func() bool {
			select {
			default:
				return false
			case _, ok := <-req.events:
				// expect channel close
				return !ok
			}
		}, "expected event channel to close")
	})
}

func TestStreamReq_EOSEHandling(t *testing.T) {
	t.Run("eose emits journal", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		// simulate EOSE
		req.order(newEOSETask("peer", time.Now()))

		expectJournalEntry(t, h.journals, reflect.TypeOf(ReceivedEOSEJournal{}))
	})
}

func TestStreamReq_ClosedHandling(t *testing.T) {
	t.Run("closed forwards message once", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		// simulate closed
		req.order(newClosedTask("peer", time.Now(), "closed"))
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReceivedClosedJournal{}))

		// receive message
		var message ReqMessage
		Eventually(t, func() bool {
			select {
			default:
				return false
			case message = <-req.messages:
				return true
			}
		}, "expected closed message")

		assert.Equal(t, "peer", message.PeerID)
		assert.False(t, message.ReceivedAt.IsZero())
		assert.Equal(t, "closed", message.Data)

		// multiple closed emit journals
		req.order(newClosedTask("peer", time.Now(), "closed"))
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReceivedClosedJournal{}))

		// but do not emit more than one message to the caller
		Never(t, func() bool {
			select {
			default:
				return false
			case <-req.messages:
				return true
			}
		}, "second closed message should not arrive")

		// close req
		req.Close()

		// expect messages channel to close
		Eventually(t, func() bool {
			select {
			default:
				return false
			case _, ok := <-req.messages:
				// expect channel close
				return !ok
			}
		}, "expected messages channel to close")
	})
}

func TestStreamReq_Reconnect(t *testing.T) {
	t.Run("req replays after reconnect", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		// initial req is sent
		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))

		// cycle disconnect-reconnect
		h.events <- PoolEvent{ID: "peer", Kind: EventDisconnected, At: time.Now()}
		Eventually(t, func() bool { return !h.isConnected("peer") },
			"expected peer to disconnect")
		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// simulate req manager handling connect event
		req.order(newHandleReconnect("peer"))

		// expect replayed req
		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 1)
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))

		// close req
		req.Close()

		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 2)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("delayed connection sends req", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		// postmaster-side connect
		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// simulate req manager handling connect event
		req.order(newHandleReconnect("peer"))

		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))

		// close req
		req.Close()

		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 1)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("no replay when closing", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)

		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		// postmaster-side connect
		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// close req
		req.Close()

		// reconnect during or after close
		req.order(newHandleReconnect("peer"))
		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})
}

func TestStreamReq_Terminal(t *testing.T) {
}

func TestStreamReq_Close(t *testing.T) {
	t.Run("close is idempotent", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// connect to peer
		h.events <- PoolEvent{ID: "peer", Kind: EventConnected, At: time.Now()}
		Eventually(t, func() bool { return h.isConnected("peer") },
			"expected peer to connect")

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)
		expectJournalEntry(t, h.journals, reflect.TypeOf(ReqSendOutcomeJournal{}))

		// close req twice
		req.Close()
		req.Close()

		// only expect one close
		expectSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 1)

		// second close never arrives
		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 2)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("close not sent if req was never sent", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedReq := envelope.EncloseReq("REQ", filters)
		expectedClose := envelope.EncloseClose("REQ")

		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedReq, 0)

		// close req
		req.Close()

		// req was never sent, so a close should not be sent
		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 0)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})

	t.Run("close not sent if req was cancelled", func(t *testing.T) {
		peers := []string{"peer"}
		h := setupReqHarness(t, peers)

		// open req
		filters := [][]byte{[]byte("{}")}
		req := NewStreamReq(
			h.ctx, "REQ", filters, peers, h.pm, h.isConnected,
			h.collector, func() { h.closed.Store(true) }, nil)

		expectedClose := envelope.EncloseClose("REQ")

		// simulate cancelled req outcome
		req.order(newReqOutcomeTask("peer", LetterOutcome{Kind: OutcomeCancelled}))

		// close req
		req.Close()

		// req was never sent, so a close should not be sent
		neverSentMessage(t, h.sent, h.sentMu, "peer", expectedClose, 0)

		Eventually(t, func() bool { return h.closed.Load() },
			"expected close callback to be called")
	})
}

func TestStreamReq_Journals(t *testing.T) {
}
