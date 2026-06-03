package prism

import (
	"testing"
)

func TestEventPublisher(t *testing.T) {
	t.Run("publish accepted", func(t *testing.T) {
		// p, envoy := newMockEnvoy(t); p.connect()
		// pub := NewEventPublisher(envoy); t.Cleanup(pub.Close)
		// go: accepted, msg, err := pub.Publish(id, json, TestTimeout)
		// Eventually: EVENT envelope on p.sent
		// p.receive(envelope.EncloseOK(id, true, ""))
		// assert accepted==true, msg=="", err==nil
	})

	t.Run("publish rejected", func(t *testing.T) {
		// same setup; feed EncloseOK(id, false, "blocked: not on allowlist")
		// assert accepted==false, msg=="blocked: not on allowlist", err==nil
	})

	t.Run("publish timeout", func(t *testing.T) {
		// pub.Publish(id, json, 75*time.Millisecond) — do not feed OK
		// assert err != nil (timeout error) after ~75ms
	})

	t.Run("concurrent correlation", func(t *testing.T) {
		// Publish A and B in separate goroutines
		// feed OK for B, then OK for A
		// assert each caller receives its own result
	})

	t.Run("publish while disconnected times out", func(t *testing.T) {
		// do not connect; Publish with short timeout
		// assert timeout error; Never: EVENT on p.sent
	})

	t.Run("held event sent on connect", func(t *testing.T) {
		// do not connect; go pub.Publish(id, json, TestTimeout)
		// Never: EVENT on p.sent (yet)
		// p.connect()
		// Eventually: EVENT on p.sent
		// p.receive(envelope.EncloseOK(id, true, ""))
		// assert caller returns (true, "", nil)
	})

	t.Run("resend after disconnect", func(t *testing.T) {
		// p.connect(); go pub.Publish
		// Eventually: first EVENT sent
		// p.disconnect(); p.connect()
		// Eventually: second EVENT sent
		// p.receive(EncloseOK); assert caller returns successfully
	})

	t.Run("send failure delivers error", func(t *testing.T) {
		// Publish; assert caller receives send error immediately (not timeout)
	})

	t.Run("disconnect then send failure on reconnect", func(t *testing.T) {
		// p.connect(); go pub.Publish (EVENT sent)
		// p.disconnect()
		// p.connect()
		// assert caller receives send error
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
