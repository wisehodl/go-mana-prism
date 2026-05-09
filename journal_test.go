package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-mana-component"
	"github.com/stretchr/testify/assert"
	"testing"
	"time"
)

type testJournalEntry struct {
	*entry
}

func newTestEntry(peerID string, comp component.Component) JournalEntry {
	return &testJournalEntry{entry: newEntry(peerID, comp)}
}

func TestJournalCollector_SingleProducer(t *testing.T) {
	jc := NewJournalCollector()
	ch := make(chan JournalEntry, 10)
	jc.Enroll(ch)

	ctx := component.MustNew(context.Background(), "test", "emitter")
	comp, _ := component.Get(ctx)
	e1 := newTestEntry("peer1", comp)
	e2 := newTestEntry("peer2", comp)

	ch <- e1
	ch <- e2
	close(ch)

	var received []JournalEntry
	out := jc.Out()

	// Wait for entries
	Eventually(t, func() bool {
		select {
		case e := <-out:
			received = append(received, e)
		default:
		}
		return len(received) == 2
	}, "should receive all entries")
}

func TestJournalCollector_MultipleProducers(t *testing.T) {
	jc := NewJournalCollector()
	ch1 := make(chan JournalEntry, 5)
	ch2 := make(chan JournalEntry, 5)
	jc.Enroll(ch1)
	jc.Enroll(ch2)

	ctx := component.MustNew(context.Background(), "test", "emitter")
	comp, _ := component.Get(ctx)

	ch1 <- newTestEntry("p1", comp)
	ch2 <- newTestEntry("p2", comp)
	ch1 <- newTestEntry("p3", comp)
	ch2 <- newTestEntry("p4", comp)

	close(ch1)
	close(ch2)

	count := 0
	out := jc.Out()
	Eventually(t, func() bool {
		select {
		case <-out:
			count++
		default:
		}
		return count == 4
	}, "should merge entries from all producers")
}

func TestJournalCollector_EnrollAfterClose(t *testing.T) {
	jc := NewJournalCollector()
	jc.Close()

	ch := make(chan JournalEntry)
	err := jc.Enroll(ch)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "closing")
}

func TestJournalCollector_CloseBlocks(t *testing.T) {
	jc := NewJournalCollector()
	ch := make(chan JournalEntry)
	jc.Enroll(ch)

	closed := make(chan struct{})
	go func() {
		jc.Close()
		close(closed)
	}()

	// Output (Out()) should still be open because the producer (ch) is open
	select {
	case <-jc.Out():
		t.Fatal("output channel closed prematurely")
	case <-time.After(NegativeTestTimeout):
	}

	// Output should not be reached yet
	select {
	case <-closed:
		t.Fatal("Close() returned before producer closed")
	default:
	}

	close(ch)

	Eventually(t, func() bool {
		select {
		case _, ok := <-jc.Out():
			return !ok
		default:
			return false
		}
	}, "Out() should close after all producers close")

	Eventually(t, func() bool {
		select {
		case <-closed:
			return true
		default:
			return false
		}
	}, "Close() should return after producers finish")
}

func TestJournalCollector_ComponentIdentity(t *testing.T) {
	jc := NewJournalCollector()
	ch := make(chan JournalEntry, 1)
	jc.Enroll(ch)

	mod := "test-mod"
	path := "a.b.c"
	ctx := component.MustNew(context.Background(), mod, path)
	comp, _ := component.Get(ctx)

	entry := newTestEntry("peer-id", comp)
	ch <- entry
	close(ch)

	out := jc.Out()
	var received JournalEntry
	Eventually(t, func() bool {
		select {
		case e := <-out:
			received = e
			return true
		default:
			return false
		}
	}, "should receive the entry")

	typed, ok := received.(*testJournalEntry)
	assert.True(t, ok, "should be correct concrete type")
	assert.Equal(t, mod, typed.Component().Module())
	assert.Equal(t, path, typed.Component().PathString())

	jc.Close()
}
