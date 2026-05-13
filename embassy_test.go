package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"sync/atomic"
	"testing"
	"time"
)

func TestEmbassy_TEMPLATE(t *testing.T) {
	ctx := context.Background()
	url := "wss://test"

	connect := func(url string) error { return nil }
	remove := func(url string) error { return nil }
	sent := false
	_ = sent
	send := func(url string, data []byte) error {
		sent = true
		return nil
	}
	events := make(chan honeybee.OutboundPoolEvent)
	inbox := make(chan honeybee.InboxMessage)
	pool := EmbassyPlugin{
		Connect: connect,
		Remove:  remove,
		Send:    send,
		Events:  events,
		Inbox:   inbox,
	}

	embassy := NewEmbassy(ctx, pool, nil)
	embassy.Dispatch(url)
	envoy := embassy.Call(url)
	assert.NotNil(t, envoy)
}

func TestEmbassy_Dispatch(t *testing.T) {
	ctx := context.Background()
	url := "wss://test"

	connectCalled := make(chan struct{})
	removeCalled := make(chan struct{})
	connect := func(url string) error { close(connectCalled); return nil }
	remove := func(url string) error { close(removeCalled); return nil }
	sent := false
	send := func(url string, data []byte) error {
		sent = true
		return nil
	}
	events := make(chan honeybee.OutboundPoolEvent)
	inbox := make(chan honeybee.InboxMessage)
	pool := EmbassyPlugin{
		Connect: connect,
		Remove:  remove,
		Send:    send,
		Events:  events,
		Inbox:   inbox,
	}

	embassy := NewEmbassy(ctx, pool, nil)
	embassy.Dispatch(url)
	envoy := embassy.Call(url)
	assert.NotNil(t, envoy)

	_, ok := <-connectCalled
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

	events <- honeybee.OutboundPoolEvent{
		ID: url, Kind: honeybee.OutboundEventConnected, At: time.Now()}
	events <- honeybee.OutboundPoolEvent{
		ID: "wss://other", Kind: honeybee.OutboundEventConnected, At: time.Now()}
	inbox <- honeybee.InboxMessage{
		ID:         url,
		Data:       envelope.EncloseEvent([]byte("{}")),
		ReceivedAt: time.Now(),
	}
	inbox <- honeybee.InboxMessage{
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
	assert.True(t, sent)

	envoy.Dismiss()

	_, ok = <-removeCalled
	assert.False(t, ok)

	_, ok = <-eventDone
	assert.False(t, ok)

	_, ok = <-inboxDone
	assert.False(t, ok)

	// envoy no longer in embassy
	envoy = embassy.Call(url)
	assert.Nil(t, envoy)
}
