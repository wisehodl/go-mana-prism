package prism

import (
	"context"
	"errors"
	"sync"
	"time"

	"git.wisehodl.dev/jay/go-roots-ws"
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
	mu        sync.Mutex
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
	events <-chan PoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
}

func NewEventPublisher(e *Envoy) *EventPublisher {
	ctx, cancel := context.WithCancel(e.Context())
	p := &EventPublisher{
		envoy:   e,
		pending: make(map[string]*pendingEntry),
		inbox:   e.SubscribeInbox([]string{"OK"}),
		events:  e.SubscribeEvents(),
		ctx:     ctx,
		cancel:  cancel,
	}
	p.wg.Go(p.routeInbox)
	p.wg.Go(p.handleEvents)
	return p
}

func (p *EventPublisher) Publish(eventID string, eventJSON []byte, timeout time.Duration) (bool, string, error) {
	entry := &pendingEntry{
		eventID:   eventID,
		eventJSON: eventJSON,
		result:    make(chan publishResult, 1),
	}

	entry.timer = time.AfterFunc(timeout, func() {
		p.mu.Lock()
		p.deregister(eventID)
		p.mu.Unlock()
		p.envoy.Observer().Record(p.envoy.URL(),
			PublishTimeout{EventID: eventID, At: time.Now()})
		p.deliver(entry, publishResult{err: errors.New("publish timeout")})
	})

	p.mu.Lock()
	p.pending[eventID] = entry
	connected := p.envoy.IsConnected()
	p.mu.Unlock()

	if connected {
		if err := p.trySend(entry); err != nil {
			p.mu.Lock()
			p.deregister(eventID)
			p.mu.Unlock()
			p.deliver(entry, publishResult{err: err})
		}
	}

	r := <-entry.result
	return r.accepted, r.message, r.err
}

func (p *EventPublisher) Close() {
	p.cancel()

	p.mu.Lock()
	for id, e := range p.pending {
		p.deregister(id)
		p.deliver(e, publishResult{err: errors.New("publisher closed")})
	}
	p.mu.Unlock()

	p.wg.Wait()
}

func (p *EventPublisher) trySend(entry *pendingEntry) error {
	entry.mu.Lock()
	defer entry.mu.Unlock()
	if entry.sent {
		return nil
	}

	err := p.envoy.Send([]byte(envelope.EncloseEvent(entry.eventJSON)))
	if err != nil {
		p.envoy.Observer().Record(p.envoy.URL(),
			PublishSendFailed{EventID: entry.eventID, Err: err, At: time.Now()})
		return err
	}
	entry.sent = true
	p.envoy.Observer().Record(p.envoy.URL(),
		PublishDispatched{EventID: entry.eventID, At: time.Now()})
	return nil
}

func (p *EventPublisher) deliver(entry *pendingEntry, result publishResult) {
	entry.once.Do(func() {
		entry.timer.Stop()
		entry.result <- result
	})
}

func (p *EventPublisher) routeInbox() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case msg, ok := <-p.inbox:
			if !ok {
				return
			}
			eventID, accepted, message, err := envelope.FindOK(msg.Data)
			if err != nil {
				continue
			}

			p.mu.Lock()
			entry, ok := p.pending[eventID]
			if ok {
				p.deregister(eventID)
			}
			p.mu.Unlock()

			if !ok {
				continue
			}

			if accepted {
				p.envoy.Observer().Record(msg.URL,
					PublishAccepted{EventID: eventID, At: time.Now()})
			} else {
				p.envoy.Observer().Record(msg.URL,
					PublishRejected{EventID: eventID, Message: message, At: time.Now()})
			}
			p.deliver(entry, publishResult{accepted: accepted, message: message})
		}
	}
}

func (p *EventPublisher) handleEvents() {
	for {
		select {
		case <-p.ctx.Done():
			return
		case ev, ok := <-p.events:
			if !ok {
				return
			}
			switch ev.Kind {
			case EventConnected:
				p.mu.Lock()
				var toSend []*pendingEntry
				for _, e := range p.pending {
					e.mu.Lock()
					if !e.sent {
						toSend = append(toSend, e)
					}
					e.mu.Unlock()
				}
				p.mu.Unlock()

				for _, e := range toSend {
					if err := p.trySend(e); err != nil {
						p.mu.Lock()
						p.deregister(e.eventID)
						p.mu.Unlock()
						p.deliver(e, publishResult{err: err})
					}
				}

			case EventDisconnected:
				p.mu.Lock()
				for _, e := range p.pending {
					e.mu.Lock()
					e.sent = false
					e.mu.Unlock()
				}
				p.mu.Unlock()
			}
		}
	}
}

func (p *EventPublisher) deregister(eventID string) {
	delete(p.pending, eventID)
}
