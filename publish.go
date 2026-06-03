package prism

import (
	"context"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Observables
// ----------------------------------------------------------------------------

type PublishDispatched struct {
	EventID string
	At      time.Time
}

type PublishAccepted struct {
	EventID string
	At      time.Time
}

type PublishRejected struct {
	EventID string
	Message string
	At      time.Time
}

type PublishTimeout struct {
	EventID string
	At      time.Time
}

type PublishSendFailed struct {
	EventID string
	Err     error
	At      time.Time
}

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

type publishResult struct {
	accepted bool
	message  string
	err      error
}

type pendingEntry struct {
	eventID   string
	eventJSON []byte
	sent      bool
	result    chan publishResult
	once      sync.Once
	timer     *time.Timer
}

// ----------------------------------------------------------------------------
// Event Publisher
// ----------------------------------------------------------------------------

type EventPublisher struct {
	envoy   *Envoy
	pending map[string]*pendingEntry // event id -> pending entry

	inbox  <-chan InboxMessage
	events <-chan OutboundPoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
}

func NewEventPublisher(e *Envoy) *EventPublisher {
	// SubscribeInbox([]string{"OK"})
	// SubscribeEvents()
	// context.WithCancel from e.Context()
	// make pending map
	// wg.Add(2); go p.routeInbox(); go p.handleEvents()
	return nil
}

func (p *EventPublisher) Publish(eventID string, eventJSON []byte, timeout time.Duration) (bool, string, error) {
	// build pendingEntry with result chan (buffered 1) and sync.Once
	// compute deadline; create timer that fires deliver(timeout error) via once
	// timer should lock -> deregister -> record PublishTimeout -> unlock -> deliver
	// mu.Lock: insert into pending map
	// if envoy connected: EncloseEvent + Send
	//   on send error: deregister, deliver send error, return
	//   on success: mark sent, record PublishDispatched
	// mu.Unlock
	// block on entry.result
	// return received publishResult fields
	return false, "", nil
}

func (p *EventPublisher) Close() {
	// p.cancel()
	// mu.Lock: iterate pending, call once.Do(deliver cancellation error) for each
	// mu.Unlock
	// p.wg.Wait()
}

func (p *EventPublisher) deliver(entry *pendingEntry, result publishResult) {
	entry.once.Do(func() {
		entry.timer.Stop()
		entry.result <- result
	})
}

func (p *EventPublisher) routeInbox() {
	defer p.wg.Done()
	// select ctx.Done | inbox msg
	//   msg: envelope.FindOK → on err continue
	//         mu.Lock; look up pending by eventID
	//         if not found: mu.Unlock; continue
	//         deregister; mu.Unlock
	//         record PublishAccepted or PublishRejected
	//         deliver(entry, publishResult{accepted, message, nil})
}

func (p *EventPublisher) handleEvents() {
	defer p.wg.Done()
	// select ctx.Done | event
	//   EventConnected:
	//     mu.Lock; iterate pending where !sent
	//       Send each; on error: deregister, deliver send error, record PublishSendFailed
	//       on success: mark sent, record PublishDispatched
	//     mu.Unlock
	//   EventDisconnected:
	//     mu.Lock; mark all sent entries as unsent
	//     mu.Unlock
}

func (p *EventPublisher) deregister(eventID string) {
	delete(p.pending, eventID)
}
