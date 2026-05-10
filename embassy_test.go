package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

func TestEmbassyPoolEvents(t *testing.T) {
	ctx := context.Background()
	eventsCh := make(chan honeybee.OutboundPoolEvent)

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  eventsCh,
	}

	e := NewEmbassy(ctx, pool, nil, nil)
	sub := e.Subscribe()

	t.Run("added then removed", func(t *testing.T) {
		err := e.Dispatch("wss://test")
		assert.NoError(t, err)

		Eventually(t, func() bool {
			select {
			default:
				return false
			case ev := <-sub:
				return ev.Kind == EventAdded
			}
		}, "expected added event")

		err = e.Dismiss("wss://test")
		assert.NoError(t, err)

		Eventually(t, func() bool {
			select {
			default:
				return false
			case ev := <-sub:
				return ev.Kind == EventRemoved
			}
		}, "expected removed event")
	})

	t.Run("connected", func(t *testing.T) {
		e.Dispatch("wss://test")

		eventsCh <- honeybee.OutboundPoolEvent{
			ID:   "wss://test",
			Kind: honeybee.OutboundEventConnected,
			At:   time.Now(),
		}

		Eventually(t, func() bool {
			select {
			default:
				return false
			case ev := <-sub:
				return ev.Kind == EventConnected
			}
		}, "expected connected event")
	})

	t.Run("disconnected", func(t *testing.T) {
		e.Dispatch("wss://test")

		eventsCh <- honeybee.OutboundPoolEvent{
			ID:   "wss://test",
			Kind: honeybee.OutboundEventDisconnected,
			At:   time.Now(),
		}

		Eventually(t, func() bool {
			select {
			default:
				return false
			case ev := <-sub:
				return ev.Kind == EventDisconnected
			}
		}, "expected disconnected event")
	})
}

func TestEmbassyPeerRegistry(t *testing.T) {
	ctx := context.Background()
	eventsCh := make(chan honeybee.OutboundPoolEvent)

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  eventsCh,
	}

	e := NewEmbassy(ctx, pool, nil, nil)

	// add
	e.Dispatch("wss://test")

	url, ok := e.HasPeer("wss://test/")
	assert.Equal(t, "wss://test", url)
	assert.True(t, ok)
	assert.False(t, e.IsConnected("wss://test"))

	// connect
	eventsCh <- honeybee.OutboundPoolEvent{
		ID:   "wss://test",
		Kind: honeybee.OutboundEventConnected,
		At:   time.Now(),
	}

	Eventually(t, func() bool {
		_, exists := e.HasPeer("wss://test")
		connected := e.IsConnected("wss://test")
		return exists && connected
	}, "expected: exists, connected")

	// disconnect
	eventsCh <- honeybee.OutboundPoolEvent{
		ID:   "wss://test",
		Kind: honeybee.OutboundEventDisconnected,
		At:   time.Now(),
	}

	Eventually(t, func() bool {
		_, exists := e.HasPeer("wss://test")
		connected := e.IsConnected("wss://test")
		return exists && !connected
	}, "expected: exists, disconnected")

	// remove
	e.Dismiss("wss://test")

	_, ok = e.HasPeer("wss://test")
	assert.False(t, ok)
	assert.False(t, e.IsConnected("wss://test"))
}

func TestEmbassyPeers(t *testing.T) {
	ctx := context.Background()

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  nil,
	}

	e := NewEmbassy(ctx, pool, nil, nil)

	assert.Len(t, e.Peers(), 0)

	e.Dispatch("wss://test1")
	e.Dispatch("wss://test2")
	assert.Len(t, e.Peers(), 2)

	e.Dismiss("wss://test2")
	assert.Len(t, e.Peers(), 1)
}

func TestEmbassySubFanout(t *testing.T) {
	ctx := context.Background()
	eventsCh := make(chan honeybee.OutboundPoolEvent)

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  eventsCh,
	}

	e := NewEmbassy(ctx, pool, nil, nil)
	sub1 := e.Subscribe()
	sub2 := e.Subscribe()

	e.Dispatch("wss://test")

	Eventually(t, func() bool {
		select {
		default:
			return false
		case ev := <-sub1:
			return ev.Kind == EventAdded
		}
	}, "expected added event on sub1")

	Eventually(t, func() bool {
		select {
		default:
			return false
		case ev := <-sub2:
			return ev.Kind == EventAdded
		}
	}, "expected added event on sub2")
}

func TestEmbassyClose(t *testing.T) {
	ctx := context.Background()
	eventsCh := make(chan honeybee.OutboundPoolEvent, 1)

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  eventsCh,
	}

	e := NewEmbassy(ctx, pool, nil, nil)
	sub1 := e.Subscribe()
	sub2 := e.Subscribe()

	e.Dispatch("wss://test")

	e.Close()

	// peer gets removed
	Eventually(t, func() bool {
		select {
		default:
			return false
		case ev := <-sub1:
			return ev.ID == "wss://test" && ev.Kind == EventRemoved
		}
	}, "expected peer removed")

	Eventually(t, func() bool {
		select {
		default:
			return false
		case ev := <-sub2:
			return ev.ID == "wss://test" && ev.Kind == EventRemoved
		}
	}, "expected peer removed")

	// peer list is empty
	_, ok := e.HasPeer("wss://test")
	assert.False(t, ok)
	assert.Len(t, e.Peers(), 0)

	// subs close
	Eventually(t, func() bool {
		_, ok1 := <-sub1
		_, ok2 := <-sub2
		return !ok1 && !ok2
	}, "subs should close")
}

func TestEmbassyJournals(t *testing.T) {
	ctx := context.Background()
	jc := NewJournalCollector()
	eventsCh := make(chan honeybee.OutboundPoolEvent, 1)

	pool := EmbassyPlugin{
		Connect: func(id string) error { return nil },
		Remove:  func(id string) error { return nil },
		Send:    func(id string, data []byte) error { return nil },
		Events:  eventsCh,
	}

	e := NewEmbassy(ctx, pool, jc, nil)
	out := jc.Out()
	peer := "wss://test"

	// added
	e.Dispatch(peer)
	Eventually(t, func() bool {
		select {
		case entry := <-out:
			_, ok := entry.(PeerAddedJournal)
			return ok
		default:
			return false
		}
	}, "expected PeerAddedJournal")

	// connected
	eventsCh <- honeybee.OutboundPoolEvent{
		ID:   peer,
		Kind: honeybee.OutboundEventConnected,
		At:   time.Now(),
	}
	Eventually(t, func() bool {
		select {
		case entry := <-out:
			e, ok := entry.(PeerConnectedJournal)

			// ensure fields are correct
			peerOk := e.PeerID() == "wss://test"
			modOk := e.Component().Module() == "prism"
			pathOk := e.Component().PathString() == "embassy"

			return ok && peerOk && modOk && pathOk
		default:
			return false
		}
	}, "expected PeerConnectedJournal")

	// disconnected
	eventsCh <- honeybee.OutboundPoolEvent{
		ID:   peer,
		Kind: honeybee.OutboundEventDisconnected,
		At:   time.Now(),
	}
	Eventually(t, func() bool {
		select {
		case entry := <-out:
			_, ok := entry.(PeerDisconnectedJournal)
			return ok
		default:
			return false
		}
	}, "expected PeerDisconnectedJournal")

	// removed
	e.Dismiss(peer)
	Eventually(t, func() bool {
		select {
		case entry := <-out:
			_, ok := entry.(PeerRemovedJournal)
			return ok
		default:
			return false
		}
	}, "expected PeerRemovedJournal")

	// close embassy: closes journal channel
	e.Close()

	// Ensure jc can close now that embassy has closed its journal channel
	jcClosed := make(chan struct{})
	go func() {
		jc.Close()
		close(jcClosed)
	}()

	Eventually(t, func() bool {
		select {
		case <-jcClosed:
			return true
		default:
			return false
		}
	}, "JournalCollector.Close() should return after Embassy.Close()")
}
