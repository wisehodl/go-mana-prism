package prism

import (
	"bytes"
	"errors"
	"testing"

	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
)

func TestAuthManager(t *testing.T) {
	t.Run("challenge triggers callback and send", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))

		signed := []byte(`{"kind":22242}`)
		var calledWith string
		callback := func(challenge string) ([]byte, error) {
			calledWith = challenge
			return signed, nil
		}

		m := NewAuthManager(envoy, callback)
		t.Cleanup(m.Close)

		p.connect()
		p.receive([]byte(envelope.EncloseAuthChallenge("abc")))

		Eventually(t, func() bool { return calledWith == "abc" },
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
		// callback succeeds
		// send returns error
		// assert AuthResponseFailed recorded
	})

	t.Run("disconnect resets state", func(t *testing.T) {
		// p.connect(); feed AUTH "abc"; callback fires
		// p.disconnect()
		// assert m.challenge == "" (inspect via exported accessor or package-level test access)
		// p.connect(); feed AUTH "def"
		// assert callback fires again with "def"
	})

	t.Run("replacement challenge", func(t *testing.T) {
		// feed AUTH "abc"; feed AUTH "def" without disconnect
		// assert callback called twice; second call receives "def"
		// assert two AUTH response envelopes sent
	})

	t.Run("malformed AUTH ignored", func(t *testing.T) {
		// p.receive(envelope.EncloseAuthResponse([]byte("{}"))) — second element is object
		// Never: callback called
		// assert no panic
	})

	t.Run("close cleans up", func(t *testing.T) {
		// m.Close()
		// feed another AUTH
		// Never: callback invoked after close
	})
}
