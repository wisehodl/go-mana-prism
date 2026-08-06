package prism

import (
	"bytes"
	"errors"
	"sync"
	"testing"

	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
)

func TestAuthManager(t *testing.T) {
	t.Run("challenge triggers callback and send", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		signed := []byte(`{"kind":22242}`)
		var mu sync.Mutex
		var calledWith string
		callback := func(challenge string) ([]byte, error) {
			mu.Lock()
			defer mu.Unlock()
			calledWith = challenge
			return signed, nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))

		Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return calledWith == "abc"
		},
			"callback not called with challenge")

		Eventually(t, func() bool {
			select {
			case got := <-p.sent:
				return bytes.Contains(got, signed)
			default:
				return false
			}
		}, "AUTH response envelope not sent")

		assert.Len(t, EventsOf[ChallengeReceived](obs), 1)
		assert.Len(t, EventsOf[AuthResponseSent](obs), 1)
	})

	t.Run("callback error recorded", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		callback := func(challenge string) ([]byte, error) {
			return nil, errors.New("signing failed")
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))

		Eventually(t, func() bool {
			return len(EventsOf[AuthResponseFailed](obs)) == 1
		}, "AuthResponseFailed not recorded")

		Never(t, func() bool {
			select {
			case <-p.sent:
				return true
			default:
				return false
			}
		}, "no AUTH envelope should be sent on callback error")

		assert.Len(t, EventsOf[ChallengeReceived](obs), 1)
	})

	t.Run("send error recorded", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		callback := func(challenge string) ([]byte, error) {
			return []byte(`{"kind":22242}`), nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.setSendError(errors.New("send failed"))
		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))

		Eventually(t, func() bool {
			return len(EventsOf[AuthResponseFailed](obs)) == 1
		}, "AuthResponseFailed not recorded")
	})

	t.Run("disconnect resets state", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		var mu sync.Mutex
		var calls []string
		callback := func(challenge string) ([]byte, error) {
			mu.Lock()
			calls = append(calls, challenge)
			mu.Unlock()
			return []byte(`{"kind":22242}`), nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))
		Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(calls) == 1 && calls[0] == "abc"
		}, "first callback not fired")

		p.disconnect()
		// drain the sent channel so p.sent doesn't block the second send
		select {
		case <-p.sent:
		default:
		}

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("def")))
		Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(calls) == 2 && calls[1] == "def"
		}, "second callback not fired with new challenge")
	})

	t.Run("replacement challenge", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		var mu sync.Mutex
		var calls []string
		callback := func(challenge string) ([]byte, error) {
			mu.Lock()
			calls = append(calls, challenge)
			mu.Unlock()
			return []byte(`{"kind":22242}`), nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))
		p.receive([]byte(envelope.EncloseAuthChallenge("def")))

		Eventually(t, func() bool {
			mu.Lock()
			defer mu.Unlock()
			return len(calls) == 2 && calls[1] == "def"
		}, "callback not called twice with both challenges")

		Eventually(t, func() bool {
			return len(p.sent) >= 2
		}, "expected 2 auth responses in pool")
	})

	t.Run("malformed AUTH ignored", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		called := false
		callback := func(challenge string) ([]byte, error) {
			called = true
			return []byte(`{"kind":22242}`), nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		// EncloseAuthResponse wraps a JSON object as the second element — not a string
		p.receive([]byte(envelope.EncloseAuthResponse([]byte(`{}`))))

		Never(t, func() bool { return called }, "callback should not be invoked for malformed AUTH")
	})

	t.Run("close cleans up", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)

		called := false
		callback := func(challenge string) ([]byte, error) {
			called = true
			return []byte(`{"kind":22242}`), nil
		}

		m := NewAuthManager(envoy, callback)
		p.connect()
		m.Close()

		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))

		Never(t, func() bool { return called }, "callback should not fire after Close")
	})
}
