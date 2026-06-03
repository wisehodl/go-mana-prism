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
		p, envoy := newMockEnvoy(t)
		h := NewNoticeHandler(envoy)
		t.Cleanup(h.Close)

		p.receive([]byte("not json"))

		Never(t, func() bool {
			select {
			case <-h.Notices():
				return true
			default:
				return false
			}
		}, "notices channel should remain empty")
	})

	t.Run("observer notified", func(t *testing.T) {
		obs := &mockObserver{}
		p, envoy := newMockEnvoy(t, WithEmbassyObserver(obs))
		h := NewNoticeHandler(envoy)
		t.Cleanup(h.Close)

		p.receive([]byte(envelope.EncloseNotice("hello")))

		Eventually(t, func() bool {
			events := EventsOf[NoticeReceived](obs)
			return len(events) == 1 && events[0].Message == "hello"
		}, "NoticeReceived observable not recorded")
	})

	t.Run("close cleans up", func(t *testing.T) {
		// h.Close()
		// assert <-h.Notices() returns zero Notice, ok == false
	})
}
