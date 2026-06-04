package prism

import (
	"errors"
	"testing"

	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
)

func TestEventPublisher(t *testing.T) {
	t.Run("publish accepted", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		const id = "aaaa1111"
		eventJSON := []byte(`{"id":"aaaa1111","kind":1}`)

		type result struct {
			accepted bool
			message  string
			err      error
		}
		ch := make(chan result, 1)
		go func() {
			accepted, msg, err := pub.Publish(id, eventJSON, TestTimeout)
			ch <- result{accepted, msg, err}
		}()

		var sent []byte
		Eventually(t, func() bool {
			select {
			case sent = <-p.sent:
				return true
			default:
				return false
			}
		}, "EVENT envelope not sent")
		assert.Contains(t, string(sent), id)

		p.receive([]byte(envelope.EncloseOK(id, true, "")))

		Eventually(t, func() bool {
			select {
			case r := <-ch:
				return r.accepted && r.message == "" && r.err == nil
			default:
				return false
			}
		}, "Publish did not return accepted result")

		assert.Len(t, EventsOf[PublishDispatched](obs), 1)
		assert.Len(t, EventsOf[PublishAccepted](obs), 1)
	})

	t.Run("publish rejected", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		const id = "bbbb2222"
		eventJSON := []byte(`{"id":"bbbb2222","kind":1}`)

		type result struct {
			accepted bool
			message  string
			err      error
		}
		ch := make(chan result, 1)
		go func() {
			accepted, msg, err := pub.Publish(id, eventJSON, TestTimeout)
			ch <- result{accepted, msg, err}
		}()

		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "EVENT envelope not sent")

		p.receive([]byte(envelope.EncloseOK(id, false, "rejected: not on whitelist")))

		Eventually(t, func() bool {
			select {
			case r := <-ch:
				return !r.accepted &&
					r.message == "rejected: not on whitelist" &&
					r.err == nil
			default:
				return false
			}
		}, "Publish did not return rejected result")

		assert.Len(t, EventsOf[PublishDispatched](obs), 1)
		assert.Len(t, EventsOf[PublishRejected](obs), 1)
	})

	t.Run("publish timeout", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		const id = "cccc3333"
		eventJSON := []byte(`{"id":"cccc3333","kind":1}`)

		ch := make(chan error, 1)
		go func() {
			_, _, err := pub.Publish(id, eventJSON, NegativeTestTimeout)
			ch <- err
		}()

		Eventually(t, func() bool {
			select {
			case err := <-ch:
				return err != nil
			default:
				return false
			}
		}, "Publish did not return a timeout error")

		assert.Len(t, EventsOf[PublishDispatched](obs), 1)
		assert.Len(t, EventsOf[PublishTimeout](obs), 1)
	})

	t.Run("concurrent correlation", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		type result struct {
			accepted bool
			message  string
			err      error
		}

		chA := make(chan result, 1)
		chB := make(chan result, 1)

		go func() {
			accepted, msg, err := pub.Publish(
				"aaaa", []byte(`{"id":"aaaa"}`), TestTimeout)
			chA <- result{accepted, msg, err}
		}()
		go func() {
			accepted, msg, err := pub.Publish(
				"bbbb", []byte(`{"id":"bbbb"}`), TestTimeout)
			chB <- result{accepted, msg, err}
		}()

		// drain both EVENT envelopes before feeding OKs
		Eventually(t, func() bool { return len(p.sent) == 2 },
			"expected two EVENT envelopes")
		<-p.sent
		<-p.sent

		// feed OK for B first, then A
		p.receive([]byte(envelope.EncloseOK("bbbb", true, "b-msg")))
		p.receive([]byte(envelope.EncloseOK("aaaa", false, "a-msg")))

		Eventually(t, func() bool {
			select {
			case r := <-chA:
				return !r.accepted &&
					r.message == "a-msg" &&
					r.err == nil
			default:
				return false
			}
		}, "caller A did not receive its result")

		Eventually(t, func() bool {
			select {
			case r := <-chB:
				return r.accepted && r.message == "b-msg" && r.err == nil
			default:
				return false
			}
		}, "caller B did not receive its result")
	})

	t.Run("publish while disconnected times out", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		// do not connect

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		ch := make(chan error, 1)
		go func() {
			_, _, err := pub.Publish(
				"dddd4444", []byte(`{"id":"dddd4444"}`), NegativeTestTimeout)
			ch <- err
		}()

		Eventually(t, func() bool {
			select {
			case err := <-ch:
				return err != nil
			default:
				return false
			}
		}, "Publish did not return a timeout error")

		Never(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "EVENT envelope should not be sent while disconnected")
	})

	t.Run("held event sent on connect", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		// do not connect

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		const id = "eeee5555"
		eventJSON := []byte(`{"id":"eeee5555"}`)

		type result struct {
			accepted bool
			message  string
			err      error
		}
		ch := make(chan result, 1)
		go func() {
			accepted, msg, err := pub.Publish(id, eventJSON, TestTimeout)
			ch <- result{accepted, msg, err}
		}()

		Never(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "EVENT envelope should not be sent before connect")

		p.connect()

		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "EVENT envelope not sent after connect")

		p.receive([]byte(envelope.EncloseOK(id, true, "")))

		Eventually(t, func() bool {
			select {
			case r := <-ch:
				return r.accepted && r.message == "" && r.err == nil
			default:
				return false
			}
		}, "Publish did not return accepted result after connect")
	})

	t.Run("resend after disconnect if OK not received in time", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		const id = "ffff6666"
		eventJSON := []byte(`{"id":"ffff6666"}`)

		type result struct {
			accepted bool
			err      error
		}
		ch := make(chan result, 1)
		go func() {
			accepted, _, err := pub.Publish(id, eventJSON, TestTimeout)
			ch <- result{accepted, err}
		}()

		// first EVENT sent
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "first EVENT envelope not sent")

		p.disconnect()
		p.connect()

		// second EVENT sent after reconnect
		// because OK was not yet received
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "second EVENT envelope not sent after reconnect")

		p.receive([]byte(envelope.EncloseOK(id, true, "")))

		Eventually(t, func() bool {
			select {
			case r := <-ch:
				return r.accepted && r.err == nil
			default:
				return false
			}
		}, "Publish did not return accepted result after resend")
	})

	t.Run("send failure delivers error", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()
		p.setSendError(errors.New("send failed"))

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		ch := make(chan error, 1)
		go func() {
			_, _, err := pub.Publish(
				"gggg7777", []byte(`{"id":"gggg7777"}`), TestTimeout)
			ch <- err
		}()

		var err error
		Eventually(t, func() bool {
			select {
			case err = <-ch:
				return err != nil
			default:
				return false
			}
		}, "Publish did not return a send error")

		assert.ErrorContains(t, err, "send failed")
		assert.Len(t, EventsOf[PublishSendFailed](obs), 1)
	})

	t.Run("disconnect then send failure on reconnect", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		p.connect()

		pub := NewEventPublisher(envoy)
		t.Cleanup(pub.Close)

		ch := make(chan error, 1)
		go func() {
			_, _, err := pub.Publish(
				"hhhh8888", []byte(`{"id":"hhhh8888"}`), TestTimeout)
			ch <- err
		}()

		// first EVENT sent successfully
		Eventually(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "first EVENT envelope not sent")

		p.disconnect()
		p.setSendError(errors.New("send failed on reconnect"))
		p.connect()

		var err error
		Eventually(t, func() bool {
			select {
			case err = <-ch:
				return err != nil
			default:
				return false
			}
		}, "Publish did not return a send error after reconnect")

		assert.ErrorContains(t, err, "send failed on reconnect")
		assert.Len(t, EventsOf[PublishSendFailed](obs), 1)
	})

	t.Run("close cancels pending", func(t *testing.T) {
		// go pub.Publish (blocks)
		// pub.Close() before feeding OK
		// assert caller receives cancellation error
	})

	t.Run("unknown OK ignored", func(t *testing.T) {
		// p.receive(envelope.EncloseOK("no-such-id", true, ""))
		// assert no panic, no side effects
	})
}
