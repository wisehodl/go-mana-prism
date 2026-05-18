package prism

import (
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRequestManager_Stream(t *testing.T) {
	t.Run("sends req when connected", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
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

	t.Run("does not send req when disconnected", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
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
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, _ := m.Stream(filters)

		// drain the REQ send
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ send")

		eventA := []byte(`{"id":"a"}`)
		eventB := []byte(`{"id":"b"}`)
		eventC := []byte(`{"id":"c"}`)
		p.receive(envelope.EncloseSubscriptionEvent(id, eventA))
		p.receive(envelope.EncloseSubscriptionEvent(id, eventB))
		p.receive(envelope.EncloseSubscriptionEvent("unrelated", eventC))

		var got []ReqEvent
		Eventually(t, func() bool {
			for {
				select {
				case ev := <-events:
					got = append(got, ev)
				default:
					return len(got) >= 2
				}
			}
		}, "expected two events")

		assert.Len(t, got, 2)
		assert.Equal(t, eventA, got[0].Data)
		assert.Equal(t, eventB, got[1].Data)
	})

	t.Run("ignores eose", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed := m.Stream(filters)

		// drain the REQ send
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ send")

		p.receive(envelope.EncloseEOSE(id))

		Never(t, func() bool {
			m.mu.RLock()
			req, ok := m.reqs[id]
			m.mu.RUnlock()
			return !ok || !req.active
		}, "request should remain registered and active after eose for stream")

		Never(t, func() bool {
			select {
			case <-closed:
				return true
			default:
				return false
			}
		}, "closed should not signal on eose for stream")

		// assert a subsequent event is still forwarded
		eventA := []byte(`{"id":"a"}`)
		p.receive(envelope.EncloseSubscriptionEvent(id, eventA))

		Eventually(t, func() bool {
			select {
			case ev := <-events:
				return assert.Equal(t, eventA, ev.Data)
			default:
				return false
			}
		}, "expected event after eose")
	})

	t.Run("closed deregisters and signals caller", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed := m.Stream(filters)

		// drain the REQ send
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ send")

		p.receive(envelope.EncloseClosed(id, "error: test"))

		var got ReqClosed
		Eventually(t, func() bool {
			select {
			case got = <-closed:
				return true
			default:
				return false
			}
		}, "expected closed signal")
		assert.Equal(t, "error: test", got.Data)

		Eventually(t, func() bool {
			select {
			case _, ok := <-events:
				return !ok
			default:
				return false
			}
		}, "events channel should close after deregistration")

		Eventually(t, func() bool {
			select {
			case _, ok := <-closed:
				return !ok
			default:
				return false
			}
		}, "closed channel should close after deregistration")

		m.mu.RLock()
		_, ok := m.reqs[id]
		m.mu.RUnlock()
		assert.False(t, ok, "registration should be removed from reqs")
	})
}

func TestRequestManager_Cancel(t *testing.T) {
	t.Run("sends close and deregisters", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, _ := m.Stream(filters)

		// drain the REQ send
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ send")

		err := m.Cancel(id)
		assert.NoError(t, err)

		var got []byte
		Eventually(t, func() bool {
			select {
			case got = <-p.sent:
				return true
			default:
				return false
			}
		}, "expected CLOSE send")
		assert.Equal(t, []byte(envelope.EncloseClose(id)), got)

		m.mu.RLock()
		_, reqOk := m.reqs[id]
		m.mu.RUnlock()
		assert.False(t, reqOk, "registration should be removed from reqs")

		Eventually(t, func() bool {
			select {
			case _, ok := <-events:
				return !ok
			default:
				return false
			}
		}, "events channel should close after cancel")
	})

	t.Run("deregisters when inactive", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		// do not connect — request will not be active

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, _ := m.Stream(filters)

		err := m.Cancel(id)
		assert.NoError(t, err)

		m.mu.RLock()
		_, ok := m.reqs[id]
		m.mu.RUnlock()
		assert.False(t, ok, "registration should be removed from reqs")

		Eventually(t, func() bool {
			select {
			case _, ok := <-events:
				return !ok
			default:
				return false
			}
		}, "events channel should close after cancel")
	})

	t.Run("returns error for unknown id", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		err := m.Cancel("unknown")
		assert.Error(t, err)
	})
}

func TestRequestManager_Query(t *testing.T) {
	t.Run("returns events and nil closed on eose", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		eventData := []byte(`{"id":"abc"}`)

		go func() {
			// wait for the REQ to arrive, extract the sub ID
			reqBytes := <-p.sent
			subID, _, err := envelope.FindReq(reqBytes)
			if err != nil {
				t.Errorf("FindReq: %v", err)
				return
			}
			// inject three EVENTs then EOSE
			for range 3 {
				raw := envelope.EncloseSubscriptionEvent(subID, eventData)
				p.receive([]byte(raw))
			}
			p.receive(envelope.EncloseEOSE(subID))
		}()

		events, closed := m.Query(filters, TestTimeout)

		assert.Len(t, events, 3)
		assert.Nil(t, closed)

		// CLOSE envelope should have been sent after EOSE
		var closeEnv []byte
		select {
		case closeEnv = <-p.sent:
		case <-time.After(TestTimeout):
			t.Fatal("timed out waiting for CLOSE envelope")
		}
		closeLabel, _ := envelope.GetLabel(closeEnv)
		assert.Equal(t, "CLOSE", string(closeLabel))
	})

	t.Run("returns empty events and closed on relay closed", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		const reason = "rate-limited: slow down"

		go func() {
			reqBytes := <-p.sent
			subID, _, err := envelope.FindReq(reqBytes)
			if err != nil {
				t.Errorf("FindReq: %v", err)
				return
			}
			p.receive(envelope.EncloseClosed(subID, reason))
		}()

		events, closed := m.Query(filters, TestTimeout)

		assert.Empty(t, events)
		if assert.NotNil(t, closed) {
			assert.Equal(t, reason, closed.Data)
		}
	})

	t.Run("returns partial events on timeout", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		eventData := []byte(`{"id":"abc"}`)
		const queryTimeout = 100 * time.Millisecond

		go func() {
			reqBytes := <-p.sent
			subID, _, err := envelope.FindReq(reqBytes)
			if err != nil {
				t.Errorf("FindReq: %v", err)
				return
			}
			for range 2 {
				p.receive(envelope.EncloseSubscriptionEvent(subID, eventData))
			}
			// no EOSE, no CLOSED — Query must time out
		}()

		start := time.Now()
		events, closed := m.Query(filters, queryTimeout)
		elapsed := time.Since(start)

		assert.GreaterOrEqual(t, elapsed, queryTimeout)
		assert.Len(t, events, 2)
		assert.Nil(t, closed)
	})

	t.Run("returns nil nil when disconnected", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		// do not connect

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		events, closed := m.Query([][]byte{[]byte(`{}`)}, TestTimeout)

		assert.Nil(t, events)
		assert.Nil(t, closed)
	})
}

func TestRequestManager_Reconnect(t *testing.T) {
	t.Run("requests deactivate on disconnect", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		idA, _, _ := m.Stream(filters)
		idB, _, _ := m.Stream(filters)

		// drain both REQ sends
		for range 2 {
			Eventually(t, func() bool {
				select {
				case <-p.sent:
					return true
				default:
					return false
				}
			}, "expected REQ send")
		}

		p.disconnect()

		Eventually(t, func() bool {
			m.mu.RLock()
			defer m.mu.RUnlock()
			reqA, okA := m.reqs[idA]
			reqB, okB := m.reqs[idB]
			return okA && okB && !reqA.active && !reqB.active
		}, "both requests should be inactive after disconnect")
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

func TestRequestManager_Close(t *testing.T) {
	t.Run("deactivates all requests without deadlock", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		filters := [][]byte{[]byte(`{}`)}
		m.Stream(filters)
		m.Stream(filters)
		m.Stream(filters)

		// drain all three REQ sends
		for range 3 {
			Eventually(t, func() bool {
				select {
				case <-p.sent:
					return true
				default:
					return false
				}
			}, "expected REQ send")
		}

		done := make(chan struct{})
		go func() {
			m.Close()
			close(done)
		}()

		Eventually(t, func() bool {
			select {
			case <-done:
				return true
			default:
				return false
			}
		}, "Close should return without deadlock")

		m.mu.RLock()
		activeCount := 0
		for _, req := range m.reqs {
			if req.active {
				activeCount++
			}
		}
		m.mu.RUnlock()
		assert.Equal(t, 0, activeCount, "all requests should be inactive after close")
	})

	t.Run("deregisters all requests on close", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		filters := [][]byte{[]byte(`{}`)}
		_, eventsA, _ := m.Stream(filters)
		_, eventsB, _ := m.Stream(filters)

		for range 2 {
			Eventually(t, func() bool {
				select {
				case <-p.sent:
					return true
				default:
					return false
				}
			}, "expected REQ send")
		}

		m.Close()

		m.mu.RLock()
		count := len(m.reqs)
		m.mu.RUnlock()
		assert.Equal(t, 0, count, "all registrations should be removed")

		Eventually(t, func() bool {
			select {
			case _, ok := <-eventsA:
				return !ok
			default:
				return false
			}
		}, "eventsA should close after manager close")

		Eventually(t, func() bool {
			select {
			case _, ok := <-eventsB:
				return !ok
			default:
				return false
			}
		}, "eventsB should close after manager close")
	})
}
