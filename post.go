package prism

import (
	"container/list"
	"context"
	"fmt"
	"git.wisehodl.dev/jay/go-mana-component"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
)

// ----------------------------------------------------------------------------
// Types
// ----------------------------------------------------------------------------

// Letters

type LetterID = uint64

type OutboundLetter struct {
	id     uint64
	peerID string
	data   Envelope
	ctx    context.Context
	cancel context.CancelFunc
}

type LetterOutcomeKind int

const (
	OutcomeSent LetterOutcomeKind = iota
	OutcomeExpired
	OutcomeCancelled
	OutcomeRejected
)

func (k LetterOutcomeKind) String() string {
	switch k {
	case OutcomeSent:
		return "sent"
	case OutcomeExpired:
		return "expired"
	case OutcomeCancelled:
		return "cancelled"
	case OutcomeRejected:
		return "rejected"
	default:
		return "unknown"
	}
}

type LetterOutcome struct {
	LetterID uint64
	PeerID   string
	Kind     LetterOutcomeKind
	SentAt   time.Time
	MissedAt time.Time
	Retries  int
}

// Postmaster

type Postmaster struct {
	couriers map[string]*Courier
	events   <-chan PoolEvent // Adapter.Subscribe
	send     PoolSendFunc     // Adapter.Send
	counter  atomic.Uint64

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	cfg    postmasterConfig
	logger *slog.Logger
}

// Courier

type Courier struct {
	cmd      chan courierCommand
	sendFunc func(data Envelope) error

	// state
	queue     list.List
	connected bool
	sending   bool

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

// Commands

type courierCommand interface {
	apply(c *Courier)
}

// Options

type PostmasterOption func(*postmasterConfig)

type postmasterConfig struct{}

// ----------------------------------------------------------------------------
// Postmaster
// ----------------------------------------------------------------------------

func NewPostmaster(
	pool *Adapter,
	send PoolSendFunc,
	opts ...PostmasterOption,
) *Postmaster {
	return nil
}

func (m *Postmaster) Send(
	ctx context.Context,
	peerID string,
	data Envelope,
	deadline time.Duration,
	onOutcome func(LetterOutcome), // should be non-blocking
) (LetterID, error) {
	return 0, nil
}

func (m *Postmaster) Close() {}

// ----------------------------------------------------------------------------
// Courier
// ----------------------------------------------------------------------------

// Traveller

type letterTraveller struct {
	letter    OutboundLetter
	onOutcome func(LetterOutcome)

	sentAt   time.Time
	missedAt time.Time
	retries  int
	once     sync.Once
}

func (t *letterTraveller) isCancelled() bool {
	return t.letter.ctx.Err() != nil
}

func (t *letterTraveller) countRetry()              { t.retries++ }
func (t *letterTraveller) setSentAt(at time.Time)   { t.sentAt = at }
func (t *letterTraveller) setMissedAt(at time.Time) { t.missedAt = at }

// Courier

func NewCourier(
	ctx context.Context,
	sendFunc func(data Envelope) error, // func => PoolSendFunc(id)
	handler slog.Handler,
) *Courier {
	ctx, cancel := context.WithCancel(
		component.MustExtend(ctx, "courier"))

	c := &Courier{
		cmd:      make(chan courierCommand, 64),
		sendFunc: sendFunc,
		ctx:      ctx,
		cancel:   cancel,
	}

	if handler != nil {
		comp, ok := component.Get(ctx)
		if ok {
			c.logger = slog.New(handler).With(slog.Any("component", comp))
		}
	}

	c.wg.Add(1)
	go c.run()

	return c
}

func (c *Courier) Enqueue(letter OutboundLetter, onOutcome func(LetterOutcome)) {
	traveller := &letterTraveller{
		letter:    letter,
		onOutcome: onOutcome,
	}
	c.command(cmdEnqueue{traveller: traveller})
}

func (c *Courier) HandleConnect() {
	c.command(cmdHandleConnect{})
}

func (c *Courier) HandleDisconnect() {
	c.command(cmdHandleDisconnect{})
}

func (c *Courier) Close() {
	c.command(&cmdCloseCourier{})
	c.cancel()
	c.wg.Wait()
}

// Internal

func (c *Courier) command(cmd courierCommand) {
	select {
	case <-c.ctx.Done():
		fmt.Println("here")
	case c.cmd <- cmd:
	}
}

func (c *Courier) run() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case cmd := <-c.cmd:
			cmd.apply(c)
			c.maybeSend()
		}
	}
}

func (c *Courier) maybeSend() {
	if !c.preflight() {
		c.drain()
		return
	}

	t, ok := c.pop()
	if !ok {
		return
	}
	c.sending = true

	c.wg.Add(1)
	go c.sendOnce(t)

}

func (c *Courier) sendOnce(t *letterTraveller) {
	defer c.wg.Done()
	err := c.sendFunc(t.letter.data)
	c.command(cmdHandleSendResult{traveller: t, at: time.Now(), err: err})
}

func (c *Courier) doneOnce(t *letterTraveller) {
	var kind LetterOutcomeKind
	if t.isCancelled() {
		// letter was cancelled
		if t.letter.ctx.Err() == context.DeadlineExceeded {
			// letter expired
			kind = OutcomeExpired
		} else {
			// letter was cancelled externally
			kind = OutcomeCancelled
		}
	} else {
		// letter was sent
		kind = OutcomeSent
	}

	outcome := LetterOutcome{
		LetterID: t.letter.id,
		PeerID:   t.letter.peerID,
		Kind:     kind,
		SentAt:   t.sentAt,
		MissedAt: t.missedAt,
		Retries:  t.retries,
	}

	t.once.Do(func() {
		t.letter.cancel()
		go t.onOutcome(outcome)
	})
}

// Helpers

func (c *Courier) preflight() bool {
	isConnected := c.connected
	notAlreadySending := !c.sending
	hasQueuedLetters := c.queue.Len() > 0
	return isConnected && notAlreadySending && hasQueuedLetters
}

func (c *Courier) drain() {
	for {
		front := c.queue.Front()
		if front == nil {
			return
		}

		t := front.Value.(*letterTraveller)
		if !t.isCancelled() {
			return
		}

		t.setMissedAt(time.Now())
		c.doneOnce(t)
		c.queue.Remove(front)
	}
}

func (c *Courier) pop() (*letterTraveller, bool) {
	for {
		front := c.queue.Front()
		if front == nil {
			return nil, false
		}

		t := front.Value.(*letterTraveller)
		c.queue.Remove(front)

		if !t.isCancelled() {
			return t, true
		}

		t.setMissedAt(time.Now())
		c.doneOnce(t)
	}
}

// ----------------------------------------------------------------------------
// Commands
// ----------------------------------------------------------------------------

type cmdEnqueue struct{ traveller *letterTraveller }

func (cmd cmdEnqueue) apply(c *Courier) {
	c.queue.PushBack(cmd.traveller)
}

type cmdHandleConnect struct{}

func (cmd cmdHandleConnect) apply(c *Courier) {
	c.connected = true
}

type cmdHandleDisconnect struct{}

func (cmd cmdHandleDisconnect) apply(c *Courier) {
	c.connected = false
}

type cmdHandleSendResult struct {
	traveller *letterTraveller
	at        time.Time
	err       error
}

func (cmd cmdHandleSendResult) apply(c *Courier) {
	c.sending = false
	if cmd.err != nil {
		cmd.traveller.countRetry()
		c.queue.PushFront(cmd.traveller)
	} else {
		cmd.traveller.setSentAt(cmd.at)
		c.doneOnce(cmd.traveller)
	}
}

type cmdCloseCourier struct{}

func (cmd cmdCloseCourier) apply(c *Courier) {
	// cancel remaining letters
	for {
		t, ok := c.pop()
		if !ok {
			break
		}
		t.letter.cancel()
		t.setMissedAt(time.Now())
		c.doneOnce(t)
	}
}
