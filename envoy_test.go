package prism

import (
	"context"
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

	envoy := newEnvoy(ctx, url, terminate, nil, nil, nil, nil, nil)

	eventSub := envoy.SubscribeEvents()
	inboxSub := envoy.SubscribeInbox([]string{"A", "B"})

	envoy.Dismiss()

	mu.RLock()
	defer mu.RUnlock()
	assert.True(t, terminated)

	_, ok := <-eventSub
	assert.False(t, ok)

	_, ok = <-inboxSub
	assert.False(t, ok)
}

func TestEnvoy_Send(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	url := "wss://test"
	var sent bool
	send := func(data []byte) error {
		sent = true
		return nil
	}

	envoy := newEnvoy(ctx, url, nil, send, nil, nil, nil, nil)
	envoy.Send([]byte("hello"))

	Eventually(t, func() bool {
		return sent
	}, "should have sent")
}

func TestEnvoy_IsConnected(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	events := make(chan PoolEvent)

	envoy := newEnvoy(ctx, url, nil, nil, events, nil, nil, nil)
	eventSub := envoy.SubscribeEvents()

	gotEvents := []PoolEvent{}
	go func() {
		for ev := range eventSub {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		}
	}()

	events <- PoolEvent{
		ID: url, Kind: EventConnected, At: time.Now()}

	Eventually(t, func() bool {
		return envoy.IsConnected()
	}, "state should have toggled")
}

func TestEnvoy_Events(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	events := make(chan PoolEvent)

	envoy := newEnvoy(ctx, url, nil, nil, events, nil, nil, nil)
	eventSub := envoy.SubscribeEvents()

	gotEvents := []PoolEvent{}
	go func() {
		for ev := range eventSub {
			mu.Lock()
			gotEvents = append(gotEvents, ev)
			mu.Unlock()
		}
	}()

	events <- PoolEvent{
		ID: url, Kind: EventConnected, At: time.Now()}

	Eventually(t, func() bool {
		mu.RLock()
		defer mu.RUnlock()
		return len(gotEvents) > 0
	}, "should have gotten event")

	mu.RLock()
	assert.Equal(t, EventConnected, gotEvents[0].Kind)
	mu.RUnlock()
}

func TestEnvoy_Inbox(t *testing.T) {
	ctx := component.MustNew(context.Background(), "prism", "test")
	mu := sync.RWMutex{}
	url := "wss://test"
	inbox := make(chan InboxMessage)

	envoy := newEnvoy(ctx, url, nil, nil, nil, inbox, nil, nil)
	inboxSub := envoy.SubscribeInbox([]string{"EVENT"})

	gotInbox := []InboxMessage{}
	go func() {
		for ev := range inboxSub {
			mu.Lock()
			gotInbox = append(gotInbox, ev)
			mu.Unlock()
		}
	}()

	inbox <- InboxMessage{
		ID:         url,
		Data:       envelope.EncloseEvent([]byte("{}")),
		ReceivedAt: time.Now(),
	}
	inbox <- InboxMessage{
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
