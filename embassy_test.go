package prism

import (
	"errors"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbassy_Dispatch(t *testing.T) {
	p := newMockPool(t)

	embassy := NewEmbassy(p.ctx, p.plugin)
	embassy.Dispatch(p.url)
	envoy := embassy.Call(p.url)
	assert.NotNil(t, envoy)

	_, ok := <-p.added
	assert.False(t, ok)

	eventSub := envoy.SubscribeEvents()
	inboxSub := envoy.SubscribeInbox([]string{"EVENT"})

	gotEvent := atomic.Int64{}
	gotInbox := atomic.Int64{}
	eventDone := make(chan struct{})
	inboxDone := make(chan struct{})
	go func() {
		for range eventSub {
			gotEvent.Add(1)
		}
		close(eventDone)
	}()
	go func() {
		for range inboxSub {
			gotInbox.Add(1)
		}
		close(inboxDone)
	}()

	p.events <- honeybee.PoolEvent{
		ID: p.url, Kind: honeybee.EventConnected, At: time.Now()}
	p.events <- honeybee.PoolEvent{
		ID: "wss://other", Kind: honeybee.EventConnected, At: time.Now()}
	p.inbox <- honeybee.InboxMessage{
		ID:         p.url,
		Data:       envelope.EncloseEvent([]byte("{}")),
		ReceivedAt: time.Now(),
	}
	p.inbox <- honeybee.InboxMessage{
		ID:         "wss://other",
		Data:       envelope.EncloseEvent([]byte("{}")),
		ReceivedAt: time.Now(),
	}

	Eventually(t, func() bool { return gotEvent.Load() > 0 },
		"should have gotten event")
	Eventually(t, func() bool { return gotInbox.Load() > 0 },
		"should have gotten inbox message")
	Eventually(t, func() bool { return envoy.IsConnected() },
		"state should have toggled")
	Never(t, func() bool { return gotEvent.Load() > 1 },
		"should have only gotten one event")
	Never(t, func() bool { return gotInbox.Load() > 1 },
		"should have only gotten one inbox message")

	envoy.Send([]byte("hello"))
	Eventually(t, func() bool {
		select {
		default:
			return false
		case msg := <-p.sent:
			return string(msg) == "hello"
		}
	}, "should have sent message")

	envoy.Dismiss()

	_, ok = <-p.removed
	assert.False(t, ok)

	_, ok = <-eventDone
	assert.False(t, ok)

	_, ok = <-inboxDone
	assert.False(t, ok)

	// envoy no longer in embassy
	envoy = embassy.Call(p.url)
	assert.Nil(t, envoy)
}

func TestEmbassy_DialFailed_Routing(t *testing.T) {
	t.Run("routed to correct subscriber with Err set", func(t *testing.T) {
		p, envoy := newMockEnvoy(t)
		sub := envoy.SubscribeEvents()

		dialErr := errors.New("connection refused")
		p.dialFail(dialErr)

		var got PoolEvent
		Eventually(t, func() bool {
			select {
			case ev := <-sub:
				got = ev
				return true
			default:
				return false
			}
		}, "expected EventDialFailed to be routed to subscriber")

		assert.Equal(t, EventDialFailed, got.Kind)
		assert.ErrorIs(t, got.Err, dialErr)
		assert.Equal(t, p.url, got.ID)
	})

	t.Run("not routed to unrelated envoy subscriber", func(t *testing.T) {
		p := newMockPool(t)
		url1 := "wss://peer1"
		url2 := "wss://peer2"

		added := make(chan struct{}, 2)
		p.plugin.Connect = func(url string, opts ...honeybee.ConnectOption) error {
			added <- struct{}{}
			return nil
		}

		emb := NewEmbassy(p.ctx, p.plugin)
		emb.Dispatch(url1)
		emb.Dispatch(url2)

		envoy2 := emb.Call(url2)
		sub2 := envoy2.SubscribeEvents()

		p.dialFail(errors.New("refused"))

		Never(t, func() bool {
			select {
			case <-sub2:
				return true
			default:
				return false
			}
		}, "EventDialFailed for a different peer must not reach unrelated subscriber")
	})
}
