package prism

import (
	"testing"
)

func TestAuthManager(t *testing.T) {
	t.Run("challenge triggers callback and send", func(t *testing.T) {
		// p, envoy := newMockEnvoy(t)
		// signed := []byte(`{"kind":22242,...}`)
		// callback records invocation, returns signed
		// m := NewAuthManager(envoy, callback)
		// t.Cleanup(m.Close)
		// p.connect(); p.receive(envelope.EncloseAuthChallenge("abc"))
		// Eventually: callback called with "abc"
		// Eventually: p.sent contains AUTH response envelope wrapping signed
		// assert ChallengeReceived and AuthResponseSent recorded on observer
	})

	t.Run("callback error recorded", func(t *testing.T) {
		// callback returns errors.New("signing failed")
		// p.receive(envelope.EncloseAuthChallenge("abc"))
		// Never: anything on p.sent
		// assert ChallengeReceived and AuthResponseFailed recorded
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
