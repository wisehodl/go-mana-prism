package prism

import (
	"fmt"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"testing"
)

// Session tests exercise the session struct in isolation.
// The session is constructed directly with mock channels and callbacks.
// These tests do not go through RequestManager.
func TestRequestManager_Session(t *testing.T) {
	t.Run("sends req on start", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, false, nil)
		go s.run()

		var got []byte
		Eventually(t, func() bool {
			select {
			case got = <-h.sent:
				return true
			default:
				return false
			}
		}, "expected send")

		assert.Equal(t, []byte(h.req), got)
	})

	t.Run("terminates on failed req send", func(t *testing.T) {
		h := newMockSessionHarness()
		send := func([]byte) error { return fmt.Errorf("send failed") }

		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			send, h.terminate, false, nil)
		go s.run()

		Eventually(t, func() bool {
			select {
			case r := <-h.terminatedWith:
				return r == termSendFailed
			default:
				return false
			}
		}, "expected termSendFailed")
	})

	t.Run("ignores eose if stream", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, false, nil)
		go s.run()

		// wait for initial REQ send before proceeding
		Eventually(t, func() bool {
			select {
			case <-h.sent:
				return true
			default:
				return false
			}
		}, "expected initial send")

		h.eose <- struct{}{}

		Never(t, func() bool {
			select {
			case <-h.terminatedWith:
				return true
			default:
				return false
			}
		}, "terminate should not be called on eose for stream")
	})

	t.Run("sends close on eose if query", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, true, nil)
		go s.run()

		// drain initial REQ send
		Eventually(t, func() bool {
			select {
			case <-h.sent:
				return true
			default:
				return false
			}
		}, "expected initial REQ send")

		h.eose <- struct{}{}

		var got []byte
		Eventually(t, func() bool {
			select {
			case got = <-h.sent:
				return true
			default:
				return false
			}
		}, "expected CLOSE send")

		assert.Equal(t, []byte(envelope.EncloseClose(h.id)), got)

		Eventually(t, func() bool {
			select {
			case r := <-h.terminatedWith:
				return r == termCloseSent
			default:
				return false
			}
		}, "expected termCloseSent")
	})

	t.Run("terminates on done close", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, false, nil)
		go s.run()

		// wait for initial req
		Eventually(t, func() bool {
			select {
			case <-h.sent:
				return true
			default:
				return false
			}
		}, "expected initial send")

		// close with done
		close(h.done)
		Eventually(t, func() bool {
			select {
			case r := <-h.terminatedWith:
				return r == termExternal
			default:
				return false
			}
		}, "expected termExternal after done closed")
	})

	t.Run("terminates on context cancel", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, false, nil)
		go s.run()

		Eventually(t, func() bool {
			select {
			case <-h.sent:
				return true
			default:
				return false
			}
		}, "expected initial send")

		s.Close()

		Eventually(t, func() bool {
			select {
			case r := <-h.terminatedWith:
				return r == termExternal
			default:
				return false
			}
		}, "expected termExternal after context cancel")
	})

	t.Run("terminates on closed signal", func(t *testing.T) {
		h := newMockSessionHarness()
		s := newSession(
			h.ctx, h.id, h.req, h.eose, h.closed, h.done,
			h.send, h.terminate, false, nil)
		go s.run()

		Eventually(t, func() bool {
			select {
			case <-h.sent:
				return true
			default:
				return false
			}
		}, "expected initial send")

		h.closed <- struct{}{}

		Eventually(t, func() bool {
			select {
			case r := <-h.terminatedWith:
				return r == termReceivedClosed
			default:
				return false
			}
		}, "expected termReceivedClosed")
	})
}

func TestRequestManager_Stream(t *testing.T) {
	t.Run("spawns session and sends req when connected", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed := m.Stream(filters)

		assert.NotEmpty(t, id)
		assert.NotNil(t, events)
		assert.NotNil(t, closed)

		var got []byte
		Eventually(t, func() bool {
			select {
			case got = <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ send")

		assert.Equal(t, []byte(envelope.EncloseReq(id, filters)), got)
	})

	t.Run("registers but does not spawn session when disconnected", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		m := NewRequestManager(envoy)
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed := m.Stream(filters)

		assert.NotEmpty(t, id)
		assert.NotNil(t, events)
		assert.NotNil(t, closed)

		Never(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "send should not be called when disconnected")
	})

	t.Run("forwards events to caller", func(t *testing.T) {
		// connect, call Stream, get events channel
		// inject two EVENT envelopes for the correct sub id into mock inbox
		// inject one EVENT envelope for an unrelated sub id
		// assert exactly two events appear on the caller's events channel
	})

	t.Run("ignores eose", func(t *testing.T) {
		// connect, call Stream
		// inject an EOSE envelope for the sub id
		// assert the events channel does not close
		// assert the closed channel receives nothing
		// assert a subsequent EVENT is still forwarded
	})

	t.Run("closed deregisters and signals caller", func(t *testing.T) {
		// connect, call Stream
		// inject a CLOSED envelope with a reason string
		// assert the closed channel yields a ReqClosed with the correct message
		// assert the events channel eventually closes (buffer drained and deregistered)
		// assert the registration is removed from reqs
	})
}

func TestRequestManager_Cancel(t *testing.T) {
	t.Run("sends close, terminates session, deregisters", func(t *testing.T) {
		// connect, call Stream, hold the id
		// call Cancel(id)
		// assert mock send was called with a CLOSE envelope for the id
		// assert the session is removed from sessions
		// assert the registration is removed from reqs
		// assert the caller's events channel eventually closes
	})

	t.Run("returns error for unknown id", func(t *testing.T) {
		// call Cancel with an id that was never registered
		// assert an error is returned
	})
}

func TestRequestManager_Query(t *testing.T) {
	t.Run("returns events and nil closed on eose", func(t *testing.T) {
		// connect the envoy
		// in a goroutine: inject three EVENT envelopes then EOSE for the query sub id
		// call Query (blocks until return)
		// assert the returned slice contains exactly three events
		// assert closed is nil
		// assert mock send was called with a CLOSE envelope (closeOnEOSE behavior)
	})

	t.Run("returns empty events and closed on relay closed", func(t *testing.T) {
		// connect the envoy
		// in a goroutine: inject a CLOSED envelope before any EVENT
		// call Query
		// assert the returned slice is empty
		// assert closed is non-nil and contains the relay's reason string
	})

	t.Run("returns partial events on timeout", func(t *testing.T) {
		// connect the envoy
		// in a goroutine: inject two EVENTs then block (no EOSE, no CLOSED)
		// call Query with a short timeout
		// assert Query returns after the timeout
		// assert the returned slice contains exactly two events
		// assert closed is nil
	})

	t.Run("returns nil nil when disconnected", func(t *testing.T) {
		// do not connect the envoy
		// call Query
		// assert it returns immediately with nil events and nil closed
	})
}

func TestRequestManager_Reconnect(t *testing.T) {
	t.Run("sessions terminate on disconnect", func(t *testing.T) {
		// connect, open two streams
		// send a disconnect event into the mock events channel
		// assert both sessions are removed from sessions map
		// assert sessionWg reaches zero
	})

	t.Run("registrations survive disconnect", func(t *testing.T) {
		// connect, open two streams, hold both events and closed channels
		// send a disconnect event
		// after sessions terminate, assert both registrations remain in reqs
		// assert both events channels are still open
		// assert both closed channels are still open
	})

	t.Run("sessions respawn and resend req on reconnect", func(t *testing.T) {
		// connect, open two streams
		// disconnect, wait for sessions to terminate
		// reconnect (send connect event)
		// assert mock send is called again for each sub id (two new REQ envelopes)
	})

	t.Run("events resume on same channel after reconnect", func(t *testing.T) {
		// connect, open a stream, hold the events channel
		// disconnect, reconnect
		// inject an EVENT for the sub id
		// assert the event appears on the original events channel
		// the caller's reference to the channel is unaffected by the reconnect cycle
	})
}

func TestRequestManager_InboxRouting(t *testing.T) {
	t.Run("routes event to correct request buffer", func(t *testing.T) {
		// connect, open two streams (sub ids A and B)
		// inject an EVENT addressed to sub id A
		// assert A's events channel receives the message
		// assert B's events channel receives nothing
	})

	t.Run("drops event for unknown sub id", func(t *testing.T) {
		// connect, open a stream
		// inject an EVENT with a sub id that has no registration
		// assert no panic, no deadlock, test completes cleanly
	})

	t.Run("drops unparseable envelope", func(t *testing.T) {
		// connect, open a stream
		// inject raw bytes that are not a valid envelope
		// assert no panic, no deadlock, test completes cleanly
	})

	t.Run("routes eose to correct session", func(t *testing.T) {
		// connect, open two streams (sub ids A and B), both with closeOnEOSE = false
		// inject EOSE for sub id A
		// assert A's session receives the signal (verify via a side effect, e.g. a counter)
		// assert B's session does not receive the signal
	})

	t.Run("routes closed to session and request", func(t *testing.T) {
		// connect, open a stream
		// inject a CLOSED envelope with a reason string
		// assert the session receives the closed signal and terminates
		// assert request.closed yields a ReqClosed with the correct message
		// both must receive the message: the session reacts, the caller is informed
	})
}

func TestRequestManager_Close(t *testing.T) {
	t.Run("terminates all sessions without deadlock", func(t *testing.T) {
		// connect, open three streams
		// call manager.Close()
		// assert Close returns (does not deadlock)
		// assert all sessions are terminated (sessions map empty)
	})

	t.Run("does not deregister requests on close", func(t *testing.T) {
		// connect, open two streams
		// call manager.Close()
		// assert registrations remain in reqs
		// termExternal does not deregister; that is the caller's domain via Cancel
	})
}
