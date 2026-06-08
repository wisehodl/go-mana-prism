package prism

import (
	"errors"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestRequestManager_Options(t *testing.T) {
	t.Run("default id uses REQ label and base32 suffix", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		idA, _, _, err := m.Stream(filters)
		assert.NoError(t, err)
		idB, _, _, err := m.Stream(filters)
		assert.NoError(t, err)

		assert.Regexp(t, `^REQ:[A-Z2-7]{8}$`, idA)
		assert.Regexp(t, `^REQ:[A-Z2-7]{8}$`, idB)
		assert.NotEqual(t, idA, idB)
	})

	t.Run("WithLabel sets prefix", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		idA, _, _, err := m.Stream(filters, WithLabel("feed"))
		assert.NoError(t, err)
		idB, _, _, err := m.Stream(filters, WithLabel("profile"))
		assert.NoError(t, err)

		assert.Regexp(t, `^feed:[A-Z2-7]{8}$`, idA)
		assert.Regexp(t, `^profile:[A-Z2-7]{8}$`, idB)
		assert.NotEqual(t, idA, idB)
	})

	t.Run("WithID uses caller id", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		id, _, _, err := m.Stream(filters, WithID("my-custom-id"))
		assert.NoError(t, err)
		assert.Equal(t, "my-custom-id", id)
	})

	t.Run("WithID wins over WithLabel", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		id, _, _, err := m.Stream(filters, WithLabel("feed"), WithID("explicit"))
		assert.NoError(t, err)
		assert.Equal(t, "explicit", id)
	})

	t.Run("WithID returns error on collision", func(t *testing.T) {
		_, envoy := newMockEnvoy(t)
		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		_, _, _, err := m.Stream(filters, WithID("dup"))
		assert.NoError(t, err)

		_, _, _, err = m.Stream(filters, WithID("dup"))
		assert.Error(t, err)
	})
}

func TestRequestManager_Stream(t *testing.T) {
	t.Run("sends req when connected", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed, err := m.Stream(filters)
		assert.NoError(t, err)

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

		Eventually(t, func() bool {
			return len(EventsOf[ReqDispatched](obs)) == 1
		}, "expected ReqDispatched observable")
		dispatched := EventsOf[ReqDispatched](obs)
		assert.Equal(t, id, dispatched[0].ID)
	})

	t.Run("does not send req when disconnected", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed, err := m.Stream(filters)
		assert.NoError(t, err)

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
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, _, err := m.Stream(filters)
		assert.NoError(t, err)

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

		Eventually(t, func() bool {
			return len(EventsOf[FirstEventReceived](obs)) == 1
		}, "expected FirstEventReceived observable")
		first := EventsOf[FirstEventReceived](obs)
		assert.Equal(t, id, first[0].ID)
		assert.False(t, first[0].ReceivedAt.IsZero())
	})

	t.Run("ignores eose", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed, err := m.Stream(filters)
		assert.NoError(t, err)

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

		Eventually(t, func() bool {
			return len(EventsOf[StreamEOSEReceived](obs)) == 1
		}, "expected StreamEOSEReceived observable")
		eoseEvents := EventsOf[StreamEOSEReceived](obs)
		assert.Equal(t, id, eoseEvents[0].ID)
	})

	t.Run("closed deregisters and signals caller", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, closed, err := m.Stream(filters)
		assert.NoError(t, err)

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
			return len(EventsOf[ClosedReceived](obs)) == 1
		}, "expected ClosedReceived observable")
		closedEvents := EventsOf[ClosedReceived](obs)
		assert.Equal(t, id, closedEvents[0].ID)
		assert.Equal(t, "error: test", closedEvents[0].Message)

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

	t.Run("send failure recorded", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		// disconnect, arm send error, reconnect so the send attempt
		// happens after the error is in place
		p.disconnect()
		p.setSendError(errors.New("send failed"))
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be reconnected")

		filters := [][]byte{[]byte(`{}`)}
		id, _, _, err := m.Stream(filters)
		assert.NoError(t, err)

		Eventually(t, func() bool {
			// on connect event handler may race with Stream()
			return len(EventsOf[ReqSendFailed](obs)) >= 1
		}, "expected ReqSendFailed observable")
		failed := EventsOf[ReqSendFailed](obs)
		assert.Equal(t, id, failed[0].ID)
		assert.NotNil(t, failed[0].Err)

		Never(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "no REQ envelope should have been sent")
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
		id, events, _, streamErr := m.Stream(filters)
		assert.NoError(t, streamErr)

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
		id, events, _, streamErr := m.Stream(filters)
		assert.NoError(t, streamErr)

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
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		eventData := []byte(`{"id":"abc"}`)

		var querySubID string
		go func() {
			// wait for the REQ to arrive, extract the sub ID
			reqBytes := <-p.sent
			subID, _, err := envelope.FindReq(reqBytes)
			if err != nil {
				t.Errorf("FindReq: %v", err)
				return
			}
			querySubID = subID
			// inject three EVENTs then EOSE
			for range 3 {
				raw := envelope.EncloseSubscriptionEvent(subID, eventData)
				p.receive([]byte(raw))
			}
			p.receive(envelope.EncloseEOSE(subID))
		}()

		events, closed, err := m.Query(filters, TestTimeout)
		assert.NoError(t, err)

		assert.Len(t, events, 3)
		assert.Nil(t, closed)

		Eventually(t, func() bool {
			return len(EventsOf[QueryEOSEReceived](obs)) == 1
		}, "expected QueryEOSEReceived observable")
		qeose := EventsOf[QueryEOSEReceived](obs)
		assert.Equal(t, querySubID, qeose[0].ID)

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

		events, closed, err := m.Query(filters, TestTimeout)
		assert.NoError(t, err)

		assert.Empty(t, events)
		if assert.NotNil(t, closed) {
			assert.Equal(t, reason, closed.Data)
		}
	})

	t.Run("returns partial events on timeout", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
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
		events, closed, err := m.Query(filters, queryTimeout)
		elapsed := time.Since(start)
		assert.NoError(t, err)

		assert.GreaterOrEqual(t, elapsed, queryTimeout)
		assert.Len(t, events, 2)
		assert.Nil(t, closed)

		missed := EventsOf[MissedEOSE](obs)
		assert.Len(t, missed, 1)
	})

	t.Run("connects within timeout and returns events", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		// do NOT connect

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })

		filters := [][]byte{[]byte(`{}`)}
		eventData := []byte(`{"id":"abc"}`)

		go func() {
			// wait to connect
			time.Sleep(50 * time.Millisecond)
			p.connect()

			// listen for REQ on pool side to extract subscription id
			reqBytes := <-p.sent
			subID, _, err := envelope.FindReq(reqBytes)
			if err != nil {
				t.Errorf("FindReq: %v", err)
				return
			}

			// send event and eose
			p.receive(envelope.EncloseSubscriptionEvent(subID, eventData))
			p.receive(envelope.EncloseEOSE(subID))
		}()

		// start the query while the peer is disconnected
		// because it connects within the timeout, the query should still
		// return events
		events, closed, err := m.Query(filters, TestTimeout)
		assert.NoError(t, err)
		assert.Len(t, events, 1)
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
		idA, _, _, _ := m.Stream(filters)
		idB, _, _, _ := m.Stream(filters)

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
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		idA, eventsA, closedA, _ := m.Stream(filters)
		idB, eventsB, closedB, _ := m.Stream(filters)

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

		m.mu.RLock()
		_, okA := m.reqs[idA]
		_, okB := m.reqs[idB]
		m.mu.RUnlock()
		assert.True(t, okA, "registration A should still exist after disconnect")
		assert.True(t, okB, "registration B should still exist after disconnect")

		Never(t, func() bool {
			select {
			case _, ok := <-eventsA:
				return !ok
			default:
				return false
			}
		}, "eventsA should remain open after disconnect")

		Never(t, func() bool {
			select {
			case _, ok := <-eventsB:
				return !ok
			default:
				return false
			}
		}, "eventsB should remain open after disconnect")

		Never(t, func() bool {
			select {
			case _, ok := <-closedA:
				return !ok
			default:
				return false
			}
		}, "closedA should remain open after disconnect")

		Never(t, func() bool {
			select {
			case _, ok := <-closedB:
				return !ok
			default:
				return false
			}
		}, "closedB should remain open after disconnect")
	})

	t.Run("requests respawn and resend req on reconnect", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		idA, _, _, _ := m.Stream(filters)
		idB, _, _, _ := m.Stream(filters)

		for range 2 {
			Eventually(t, func() bool {
				select {
				case <-p.sent:
					return true
				default:
					return false
				}
			}, "expected initial REQ send")
		}

		p.disconnect()
		Eventually(t, func() bool {
			m.mu.RLock()
			defer m.mu.RUnlock()
			reqA, okA := m.reqs[idA]
			reqB, okB := m.reqs[idB]
			return okA && okB && !reqA.active && !reqB.active
		}, "both requests should be inactive after disconnect")

		p.connect()

		var sentIDs []string
		for range 2 {
			Eventually(t, func() bool {
				select {
				case raw := <-p.sent:
					subID, _, err := envelope.FindReq(raw)
					if err == nil {
						sentIDs = append(sentIDs, subID)
					}
					return err == nil
				default:
					return false
				}
			}, "expected REQ resend after reconnect")
		}

		assert.ElementsMatch(t, []string{idA, idB}, sentIDs)

		Eventually(t, func() bool {
			m.mu.RLock()
			defer m.mu.RUnlock()
			reqA, okA := m.reqs[idA]
			reqB, okB := m.reqs[idB]
			return okA && okB && reqA.active && reqB.active
		}, "both requests should be active after reconnect")
	})

	t.Run("events resume on same channel after reconnect", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		t.Cleanup(func() { m.Close() })
		filters := [][]byte{[]byte(`{}`)}
		id, events, _, _ := m.Stream(filters)

		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected initial REQ send")

		p.disconnect()
		Eventually(t, func() bool {
			m.mu.RLock()
			defer m.mu.RUnlock()
			req, ok := m.reqs[id]
			return ok && !req.active
		}, "request should be inactive after disconnect")

		p.connect()
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "expected REQ resend after reconnect")

		eventData := []byte(`{"id":"z"}`)
		p.receive(envelope.EncloseSubscriptionEvent(id, eventData))

		Eventually(t, func() bool {
			select {
			case ev := <-events:
				return assert.Equal(t, eventData, ev.Data)
			default:
				return false
			}
		}, "event should arrive on original channel after reconnect")
	})
}

func TestRequestManager_Close(t *testing.T) {
	t.Run("deactivates all requests without deadlock", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()
		Eventually(t, envoy.IsConnected, "envoy should be connected")

		m := NewRequestManager(envoy)
		filters := [][]byte{[]byte(`{}`)}
		_, _, _, _ = m.Stream(filters)
		_, _, _, _ = m.Stream(filters)
		_, _, _, _ = m.Stream(filters)

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
		_, eventsA, _, _ := m.Stream(filters)
		_, eventsB, _, _ := m.Stream(filters)

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
