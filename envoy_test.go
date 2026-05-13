package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"github.com/stretchr/testify/assert"
	"sync"
	"testing"
	"time"
)

func TestEnvoy_Dismiss(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	terminated := false
	terminate := func() {
		mu.Lock()
		defer mu.Unlock()
		terminated = true
	}

	envoy := newEnvoy(ctx, url, terminate, nil, nil, nil, nil)
	envoy.Dismiss()

	mu.RLock()
	defer mu.RUnlock()
	assert.True(t, terminated)
}

func TestEnvoy_Send(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	url := "wss://test"
	var sent bool
	send := func(data []byte) error {
		sent = true
		return nil
	}

	envoy := newEnvoy(ctx, url, nil, send, nil, nil, nil)
	envoy.Send([]byte("hello"))

	Eventually(t, func() bool {
		return sent
	}, "should have sent")
}

func TestEnvoy_IsConnected(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	events := make(chan honeybee.OutboundPoolEvent)

	envoy := newEnvoy(ctx, url, nil, nil, events, nil, nil)
	eventSub := envoy.SubscribeEvents()

	gotEvents := []honeybee.OutboundPoolEvent{}
	go func() {
		for ev := range eventSub {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		}
	}()

	events <- honeybee.OutboundPoolEvent{
		ID: url, Kind: honeybee.OutboundEventConnected, At: time.Now()}

	Eventually(t, func() bool {
		return envoy.IsConnected()
	}, "state should have toggled")
}

func TestEnvoy_Events(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	events := make(chan honeybee.OutboundPoolEvent)

	envoy := newEnvoy(ctx, url, nil, nil, events, nil, nil)
	eventSub := envoy.SubscribeEvents()

	gotEvents := []honeybee.OutboundPoolEvent{}
	go func() {
		for ev := range eventSub {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		}
	}()

	events <- honeybee.OutboundPoolEvent{
		ID: url, Kind: honeybee.OutboundEventConnected, At: time.Now()}

	Eventually(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()
		return len(gotEvents) > 0
	}, "should have gotten event")

	mu.RLock()
	assert.Equal(t, honeybee.OutboundEventConnected, gotEvents[0].Kind)
	mu.RUnlock()
}

func TestEnvoy_Inbox(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	inbox := make(chan honeybee.InboxMessage)

	envoy := newEnvoy(ctx, url, nil, nil, nil, inbox, nil)
	inboxSub := envoy.SubscribeInbox([]string{"EVENT"})

	gotInbox := []honeybee.InboxMessage{}
	go func() {
		for ev := range inboxSub {
			mu.Lock()
			gotInbox = append(gotInbox, ev)
			mu.Unlock()
		}
	}()

	inbox <- honeybee.InboxMessage{
		ID:         url,
		Data:       envelope.EncloseEvent([]byte("{}")),
		ReceivedAt: time.Now(),
	}
	inbox <- honeybee.InboxMessage{
		ID:         url,
		Data:       envelope.EncloseOK("id", true, "ok"),
		ReceivedAt: time.Now(),
	}

	Eventually(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()
		return len(gotInbox) > 0
	}, "should have gotten inbox message")

	// should only receive the EVENT message
	assert.Len(t, gotInbox, 1)

	mu.RLock()
	data, err := envelope.FindEvent(gotInbox[0].Data)
	mu.RUnlock()
	assert.NoError(t, err)
	assert.Equal(t, "{}", string(data))
}
