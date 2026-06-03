package prism

import (
	"testing"

	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
)

func TestNoticeHandler(t *testing.T) {
	t.Run("notice delivered", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		h := NewNoticeHandler(envoy)
		t.Cleanup(h.Close)

		p.receive([]byte(envelope.EncloseNotice("hello")))

		var got Notice
		Eventually(t, func() bool {
			select {
			case got = <-h.Notices():
				return true
			default:
				return false
			}
		}, "notice not delivered")

		assert.Equal(t, "hello", got.Message)
		assert.Equal(t, p.url, got.PeerID)
		assert.False(t, got.Timestamp.IsZero())
	})

	t.Run("malformed ignored", func(t *testing.T) {
		// p.receive([]byte("not json"))
		// Never: nothing readable from h.Notices() within NegativeTestTimeout
		// assert no panic
	})

	t.Run("observer notified", func(t *testing.T) {
		// wire mockObserver via envoy (requires constructor option or mock envoy)
		// p.receive(envelope.EncloseNotice("hello"))
		// Eventually: mockObserver recorded NoticeReceived{Message: "hello"}
	})

	t.Run("close cleans up", func(t *testing.T) {
		// h.Close()
		// assert <-h.Notices() returns zero Notice, ok == false
	})
}
