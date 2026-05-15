package prism

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
)

// ----------------------------------------------------------------------------
// Session
// ----------------------------------------------------------------------------

func TestSession_SendFailure_Terminates(t *testing.T) {
	sendFn := func(data []byte) error {
		return fmt.Errorf("write error")
	}

	terminateCalled := make(chan struct{})
	terminate := func() { close(terminateCalled) }

	done := make(chan struct{})
	s := newSession("TESTID01", []byte(`["REQ"]`), sendFn, done, terminate)
	go s.run()

	Eventually(t, func() bool {
		select {
		case <-terminateCalled:
			return true
		default:
			return false
		}
	}, "terminate should be called on send failure")
}

// ----------------------------------------------------------------------------
// Query
// ----------------------------------------------------------------------------

func TestQuery_ReturnsEventsBeforeEOSE(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}

	var events []ReqEvent
	var queryDone = make(chan struct{})
	go func() {
		events, _ = h.manager.Query(filters, TestTimeout)
		close(queryDone)
	}()

	// Intercept the outbound REQ to learn the sub-ID.
	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendEvent(subID, []byte(`{"id":"aaa"}`))
	h.sendEvent(subID, []byte(`{"id":"bbb"}`))
	h.sendEOSE(subID)

	Eventually(t, func() bool {
		select {
		case <-queryDone:
			return true
		default:
			return false
		}
	}, "query should return after EOSE")

	assert.Len(t, events, 2)
}

func TestQuery_EOSE_SendsCLOSEAndReturns(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}

	var events []ReqEvent
	queryDone := make(chan struct{})
	go func() {
		events, _ = h.manager.Query(filters, TestTimeout)
		close(queryDone)
	}()

	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendEOSE(subID)

	Eventually(t, func() bool {
		select {
		case <-queryDone:
			return true
		default:
			return false
		}
	}, "query should return after EOSE")

	assert.Empty(t, events)

	// Verify a CLOSE was sent after EOSE.
	Eventually(t, func() bool {
		select {
		case msg := <-h.sent:
			closedID, err := envelope.FindClose(msg)
			return err == nil && closedID == subID
		default:
			return false
		}
	}, "manager should send CLOSE after EOSE")
}

func TestQuery_CLOSED_ReturnsMessageAndEmptyEvents(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}

	var retEvents []ReqEvent
	var retClosed *ReqClosed
	queryDone := make(chan struct{})
	go func() {
		retEvents, retClosed = h.manager.Query(filters, TestTimeout)
		close(queryDone)
	}()

	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendClosed(subID, "rate limited: too many subscriptions")

	Eventually(t, func() bool {
		select {
		case <-queryDone:
			return true
		default:
			return false
		}
	}, "query should return after CLOSED")

	assert.Empty(t, retEvents)
	assert.NotNil(t, retClosed)
	assert.Equal(t, "rate limited: too many subscriptions", retClosed.Data)
}

func TestQuery_Timeout_ReturnsPartialEvents(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}

	var retEvents []ReqEvent
	queryDone := make(chan struct{})
	go func() {
		retEvents, _ = h.manager.Query(filters, 200*time.Millisecond)
		close(queryDone)
	}()

	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendEvent(subID, []byte(`{"id":"aaa"}`))
	// No EOSE — let the timeout fire.

	Eventually(t, func() bool {
		select {
		case <-queryDone:
			return true
		default:
			return false
		}
	}, "query should return after timeout")

	assert.Len(t, retEvents, 1)
}

// ----------------------------------------------------------------------------
// Disconnect / Reconnect
// ----------------------------------------------------------------------------

func TestStream_Disconnect_TerminatesSession(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	_, _, _ = h.manager.Stream(filters)

	// Wait for the REQ to be sent so we know the session is running.
	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	h.disconnect()

	// After disconnect, reqWg should reach zero, meaning the session exited.
	wgDone := make(chan struct{})
	go func() {
		h.manager.reqWg.Wait()
		close(wgDone)
	}()

	Eventually(t, func() bool {
		select {
		case <-wgDone:
			return true
		default:
			return false
		}
	}, "reqWg should reach zero after disconnect")
}

func TestStream_Disconnect_PreservesRegistration(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	id, _, _ := h.manager.Stream(filters)

	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	h.disconnect()

	wgDone := make(chan struct{})
	go func() {
		h.manager.reqWg.Wait()
		close(wgDone)
	}()
	Eventually(t, func() bool {
		select {
		case <-wgDone:
			return true
		default:
			return false
		}
	}, "reqWg should clear")

	h.manager.mu.RLock()
	_, regExists := h.manager.regs[id]
	h.manager.mu.RUnlock()

	assert.True(t, regExists, "registration should survive disconnect")
}

func TestStream_Reconnect_ResendsREQ(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	_, _, _ = h.manager.Stream(filters)

	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "first REQ should be sent")

	h.disconnect()
	wgDone := make(chan struct{})
	go func() { h.manager.reqWg.Wait(); close(wgDone) }()
	Eventually(t, func() bool {
		select {
		case <-wgDone:
			return true
		default:
			return false
		}
	}, "reqWg should clear after disconnect")

	h.connect()

	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "REQ should be resent after reconnect")
}

func TestStream_Reconnect_ResumesForwardingEvents(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	_, eventsCh, _ := h.manager.Stream(filters)

	var firstREQ []byte
	Eventually(t, func() bool {
		select {
		case firstREQ = <-h.sent:
			return true
		default:
			return false
		}
	}, "first REQ should be sent")

	firstSubID, _, err := envelope.FindReq(firstREQ)
	assert.NoError(t, err)

	h.sendEvent(firstSubID, []byte(`{"id":"before"}`))
	Eventually(t, func() bool {
		select {
		case <-eventsCh:
			return true
		default:
			return false
		}
	}, "event before disconnect should arrive")

	h.disconnect()
	wgDone := make(chan struct{})
	go func() { h.manager.reqWg.Wait(); close(wgDone) }()
	Eventually(t, func() bool {
		select {
		case <-wgDone:
			return true
		default:
			return false
		}
	}, "reqWg should clear after disconnect")

	h.connect()

	var secondREQ []byte
	Eventually(t, func() bool {
		select {
		case secondREQ = <-h.sent:
			return true
		default:
			return false
		}
	}, "second REQ should be sent after reconnect")

	secondSubID, _, err := envelope.FindReq(secondREQ)
	assert.NoError(t, err)

	h.sendEvent(secondSubID, []byte(`{"id":"after"}`))
	var gotAfter ReqEvent
	Eventually(t, func() bool {
		select {
		case gotAfter = <-eventsCh:
			return true
		default:
			return false
		}
	}, "event after reconnect should arrive on same channel")
	assert.Equal(t, []byte(`{"id":"after"}`), gotAfter.Data)
}

func TestQuery_Disconnect_ReturnsEmpty(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}

	var retEvents []ReqEvent
	queryDone := make(chan struct{})
	go func() {
		retEvents, _ = h.manager.Query(filters, TestTimeout)
		close(queryDone)
	}()

	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "manager should send REQ")

	h.disconnect()

	Eventually(t, func() bool {
		select {
		case <-queryDone:
			return true
		default:
			return false
		}
	}, "query should return after disconnect")

	assert.Empty(t, retEvents)
}

// ----------------------------------------------------------------------------
// Inbox routing
// ----------------------------------------------------------------------------

func TestRouteInbox_RoutesEventToCorrectSession(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	idA, evA, _ := h.manager.Stream([][]byte{[]byte(`{"kinds":[1]}`)})
	idB, evB, _ := h.manager.Stream([][]byte{[]byte(`{"kinds":[2]}`)})

	// Collect both outbound REQs (order is non-deterministic).
	reqs := make(map[string]bool)
	Eventually(t, func() bool {
		for {
			select {
			case msg := <-h.sent:
				subID, _, err := envelope.FindReq(msg)
				if err == nil {
					reqs[subID] = true
				}
			default:
				return len(reqs) >= 2
			}
		}
	}, "both REQs should be sent")

	assert.Contains(t, reqs, idA)
	assert.Contains(t, reqs, idB)

	// Send an event targeted at idA's subscription.
	h.sendEvent(idA, []byte(`{"id":"for-a"}`))

	var gotA ReqEvent
	Eventually(t, func() bool {
		select {
		case gotA = <-evA:
			return true
		default:
			return false
		}
	}, "event for idA should arrive on evA")
	assert.Equal(t, []byte(`{"id":"for-a"}`), gotA.Data)

	Never(t, func() bool {
		select {
		case <-evB:
			return true
		default:
			return false
		}
	}, "event for idA should not appear on evB")
}

func TestRouteInbox_DropsUnknownSubID(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	// Send an EVENT for an ID no session knows about.
	h.sendEvent("UNKNOWN1", []byte(`{"id":"ghost"}`))

	// No panic, no block, manager continues operating.
	_, evCh, _ := h.manager.Stream([][]byte{[]byte(`{"kinds":[1]}`)})

	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "REQ should be sent")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendEvent(subID, []byte(`{"id":"real"}`))
	Eventually(t, func() bool {
		select {
		case <-evCh:
			return true
		default:
			return false
		}
	}, "real event should still arrive after unknown drop")
}

func TestRouteInbox_DropsUnparseable(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	// Inject a malformed message directly into the inbox.
	h.inbox <- honeybee.InboxMessage{
		ID:         "wss://test",
		Data:       []byte(`not valid json at all`),
		ReceivedAt: time.Now(),
	}

	// Manager should still be alive and routing correctly.
	_, evCh, _ := h.manager.Stream([][]byte{[]byte(`{"kinds":[1]}`)})

	var req []byte
	Eventually(t, func() bool {
		select {
		case req = <-h.sent:
			return true
		default:
			return false
		}
	}, "REQ should be sent after malformed message")

	subID, _, err := envelope.FindReq(req)
	assert.NoError(t, err)

	h.sendEvent(subID, []byte(`{"id":"ok"}`))
	Eventually(t, func() bool {
		select {
		case <-evCh:
			return true
		default:
			return false
		}
	}, "event should arrive after malformed message was dropped")
}

// ----------------------------------------------------------------------------
// Manager lifecycle
// ----------------------------------------------------------------------------

func TestManager_StreamWhileDisconnected_SessionSpawnedOnConnect(t *testing.T) {
	h := newManagerHarness(t)
	// Do NOT connect yet.

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	_, _, _ = h.manager.Stream(filters)

	// No REQ should be sent while disconnected.
	Never(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "no REQ should be sent before connect")

	// Now connect — session should spawn and REQ should be sent.
	h.connect()
	Eventually(t, func() bool {
		select {
		case <-h.sent:
			return true
		default:
			return false
		}
	}, "REQ should be sent after connect")
}

func TestManager_QueryWhileDisconnected_ReturnsEmpty(t *testing.T) {
	h := newManagerHarness(t)
	// Do NOT connect.

	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	events, closed := h.manager.Query(filters, TestTimeout)

	assert.Empty(t, events)
	assert.Nil(t, closed)
}

func TestManager_Close_TerminatesAllSessions(t *testing.T) {
	h := newManagerHarness(t)
	h.connect()
	Eventually(t, h.envoy.IsConnected, "envoy should be connected")

	h.manager.Stream([][]byte{[]byte(`{"kinds":[1]}`)})
	h.manager.Stream([][]byte{[]byte(`{"kinds":[2]}`)})

	// Wait for both REQs.
	Eventually(t, func() bool {
		for {
			select {
			case <-h.sent:
			default:
				return len(h.manager.sessions) >= 2
			}
		}
	}, "both sessions should be active")

	closed := make(chan struct{})
	go func() {
		h.manager.Close()
		close(closed)
	}()

	Eventually(t, func() bool {
		select {
		case <-closed:
			return true
		default:
			return false
		}
	}, "manager.Close() should return")
}

// ----------------------------------------------------------------------------
// Stream (unit-level session tests)
// ----------------------------------------------------------------------------

func TestStream_ForwardsEvents(t *testing.T) {
	si := newSessionInbox()
	done := make(chan struct{})
	terminate := func() {}

	eventsCh := make(chan ReqEvent, 4)

	s := newSession(
		"TESTID01",
		[]byte(`["REQ"]`),
		func([]byte) error { return nil },
		done,
		terminate,
		withSessionInbox(si),
		withForwardEvents(eventsCh),
	)
	go s.run()

	eventData := []byte(`{"id":"abc"}`)
	si.events <- inboxEvent{
		subID:      "TESTID01",
		data:       eventData,
		receivedAt: time.Now(),
	}

	Eventually(t, func() bool { return len(eventsCh) > 0 }, "event should be forwarded")

	got := <-eventsCh
	assert.Equal(t, eventData, got.Data)

	close(done)
}

func TestStream_IgnoresEOSE(t *testing.T) {
	si := newSessionInbox()
	done := make(chan struct{})
	terminateCalled := make(chan struct{})
	terminate := func() { close(terminateCalled) }

	s := newSession(
		"TESTID01",
		[]byte(`["REQ"]`),
		func([]byte) error { return nil },
		done,
		terminate,
		withSessionInbox(si),
	)
	go s.run()

	si.eose <- inboxEOSE{subID: "TESTID01", receivedAt: time.Now()}

	Never(t, func() bool {
		select {
		case <-terminateCalled:
			return true
		default:
			return false
		}
	}, "stream session should not terminate on EOSE")

	close(done)
}

func TestStream_CLOSED_ForwardsMessageAndTerminates(t *testing.T) {
	si := newSessionInbox()
	done := make(chan struct{})
	terminateCalled := make(chan struct{})
	deregisterCalled := make(chan struct{})
	terminate := func() { close(terminateCalled) }
	deregister := func() { close(deregisterCalled) }

	forwardedClosed := make(chan ReqClosed, 1)

	s := newSession(
		"TESTID01",
		[]byte(`["REQ"]`),
		func([]byte) error { return nil },
		done,
		terminate,
		withSessionInbox(si),
		withDeregister(deregister),
		withForwardClosed(forwardedClosed),
	)
	go s.run()

	si.closed <- inboxClosed{
		subID:      "TESTID01",
		message:    "rate limited: too many subscriptions",
		receivedAt: time.Now(),
	}

	Eventually(t, func() bool {
		select {
		case <-terminateCalled:
			return true
		default:
			return false
		}
	}, "terminate should be called on CLOSED")

	Eventually(t, func() bool {
		select {
		case <-deregisterCalled:
			return true
		default:
			return false
		}
	}, "deregister should be called on CLOSED")

	assert.Len(t, forwardedClosed, 1)
	got := <-forwardedClosed
	assert.Equal(t, "rate limited: too many subscriptions", got.Data)
}

func TestStream_Cancel_SendsCLOSEAndTerminates(t *testing.T) {
	si := newSessionInbox()
	done := make(chan struct{})
	terminateCalled := make(chan struct{})
	terminate := func() { close(terminateCalled) }

	var sentMessages [][]byte
	var sentMu sync.Mutex
	sendFn := func(data []byte) error {
		sentMu.Lock()
		sentMessages = append(sentMessages, data)
		sentMu.Unlock()
		return nil
	}

	id := "TESTID01"
	s := newSession(
		id,
		[]byte(`["REQ"]`),
		sendFn,
		done,
		terminate,
		withSessionInbox(si),
	)
	go s.run()

	// Wait for REQ to be sent before cancelling.
	Eventually(t, func() bool {
		sentMu.Lock()
		defer sentMu.Unlock()
		return len(sentMessages) > 0
	}, "REQ should be sent before cancel")

	s.Close()

	Eventually(t, func() bool {
		select {
		case <-terminateCalled:
			return true
		default:
			return false
		}
	}, "terminate should be called on cancel")

	sentMu.Lock()
	defer sentMu.Unlock()
	expectedCLOSE := []byte(envelope.EncloseClose(id))
	assert.Contains(t, sentMessages, expectedCLOSE)
}

func TestSession_SendsREQOnStart(t *testing.T) {
	sent := make(chan []byte, 1)
	sendFn := func(data []byte) error {
		sent <- data
		return nil
	}

	id := "TESTID01"
	filters := [][]byte{[]byte(`{"kinds":[1]}`)}
	req := mustEncloseReq(id, filters)
	done := make(chan struct{})
	terminate := func() {}

	s := newSession(id, req, sendFn, done, terminate)
	go s.run()

	Eventually(t, func() bool { return len(sent) > 0 }, "session should send REQ")

	got := <-sent
	assert.Equal(t, req, got)

	close(done)
}
