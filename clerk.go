package prism

import (
	"context"
	"errors"
	"fmt"
	"git.wisehodl.dev/jay/go-honeybee"
	"git.wisehodl.dev/jay/go-mana-component"
	"git.wisehodl.dev/jay/go-roots-ws"
	"log/slog"
	"sync"
	"time"
)

// ----------------------------------------------------------------------------
// Errors
// ----------------------------------------------------------------------------

var (
	ErrAlreadyStarted = errors.New("clerk already started")
	ErrUnknownLabel   = errors.New("unknown label")
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

// Letters

type InboundLetter struct {
	ID   string
	Data Envelope
	At   time.Time
}

// Clerk

type Clerk struct {
	inbox <-chan honeybee.InboxMessage

	// wiring phase
	mu      sync.Mutex
	started bool
	pending []clerkSub
	known   map[string]struct{}

	// runtime phase
	routes clerkRoutes

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	logger *slog.Logger
}

type clerkSub struct {
	ch     chan InboundLetter
	labels map[string]struct{}
}

type clerkRoutes = map[string][]chan InboundLetter

// ----------------------------------------------------------------------------
// Clerk
// ----------------------------------------------------------------------------

func NewClerk(
	ctx context.Context,
	inbox <-chan honeybee.InboxMessage,
	knownLabels map[string]struct{},
	handler slog.Handler,
) *Clerk {
	ctx, cancel := context.WithCancel(
		component.MustNew(ctx, "prism", "clerk"))

	known := make(map[string]struct{}, len(knownLabels))
	for label := range knownLabels {
		known[label] = struct{}{}
	}

	c := &Clerk{
		inbox:  inbox,
		known:  known,
		ctx:    ctx,
		cancel: cancel,
	}

	if handler != nil {
		comp := component.FromContext(ctx)
		c.logger = slog.New(handler).With(slog.Any("component", comp))
	}

	return c
}

func (c *Clerk) Subscribe(
	labels map[string]struct{},
	buffer int,
) (<-chan InboundLetter, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return nil, ErrAlreadyStarted
	}

	for label := range labels {
		if _, ok := c.known[label]; !ok {
			return nil, fmt.Errorf("%w: %s", ErrUnknownLabel, label)
		}
	}

	subLabels := make(map[string]struct{}, len(labels))
	for label := range labels {
		subLabels[label] = struct{}{}
	}

	ch := make(chan InboundLetter, buffer)
	c.pending = append(c.pending, clerkSub{ch: ch, labels: subLabels})

	return ch, nil
}

func (c *Clerk) Start() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.started {
		return ErrAlreadyStarted
	}

	routes := make(clerkRoutes, len(c.known))
	for _, sub := range c.pending {
		for label := range sub.labels {
			routes[label] = append(routes[label], sub.ch)
		}
	}

	c.routes = routes
	c.started = true

	c.wg.Add(1)
	go c.run()

	return nil
}

func (c *Clerk) Close() {
	c.cancel()
	c.wg.Wait()

	c.mu.Lock()
	defer c.mu.Unlock()

	for _, sub := range c.pending {
		close(sub.ch)
	}

	// prevent double channel closes if Close() is called twice
	c.pending = nil
}

func (c *Clerk) run() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return

		case msg, ok := <-c.inbox:
			if !ok {
				// inbox closed externally, close clerk
				c.cancel()
				return
			}

			labelBytes, err := envelope.GetLabel(msg.Data)
			if err != nil {
				if c.logger != nil {
					c.logger.Warn("invalid envelope",
						"peer_id", msg.ID,
						"received_at", msg.ReceivedAt,
					)
				}
				continue
			}

			subs, ok := c.routes[string(labelBytes)]
			if !ok {
				continue
			}

			letter := InboundLetter{
				ID:   msg.ID,
				Data: msg.Data,
				At:   msg.ReceivedAt,
			}

			for _, ch := range subs {
				select {
				case ch <- letter:
				case <-c.ctx.Done():
					return
				}
			}
		}
	}
}
