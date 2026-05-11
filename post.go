package prism

import (
	"container/list"
	"context"
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
	couriers    map[string]*Courier
	poolHasPeer func(id string) (string, bool)
	poolEvents  <-chan PoolEvent // Adapter.Subscribe
	poolSend    PoolSendFunc     // Adapter.Send
	counter     atomic.Uint64

	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.RWMutex
	wg      sync.WaitGroup
	cfg     postmasterConfig
	handler slog.Handler
	logger  *slog.Logger
}

// Courier

type Courier struct {
	tasks    chan courierTask
	sendFunc func(data Envelope) error

	// state
	queue     list.List
	connected bool
	sending   bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	logger *slog.Logger
}

// Messages

type courierTask interface {
	dispatch(c *Courier)
}

// Options

const (
	DefaultPostmasterDeadline = 30 * time.Second
)

type PostmasterOption func(*postmasterConfig)

type postmasterConfig struct {
	defaultDeadline time.Duration
}

func WithDefaultDeadline(d time.Duration) PostmasterOption {
	return func(c *postmasterConfig) { c.defaultDeadline = d }
}

type SendOption func(*sendConfig)

type sendConfig struct {
	deadline time.Duration
}

func WithDeadline(d time.Duration) SendOption {
	return func(c *sendConfig) { c.deadline = d }
}

// ----------------------------------------------------------------------------
// Postmaster
// ----------------------------------------------------------------------------

func NewPostmaster(
	ctx context.Context,
	poolHasPeer func(id string) (string, bool),
	poolEvents <-chan PoolEvent,
	poolSendFunc PoolSendFunc,
	handler slog.Handler,
	opts ...PostmasterOption,
) *Postmaster {
	ctx, cancel := context.WithCancel(
		component.MustNew(ctx, "prism", "postmaster"))

	cfg := postmasterConfig{
		defaultDeadline: DefaultPostmasterDeadline,
	}
	for _, opt := range opts {
		opt(&cfg)
	}

	pm := &Postmaster{
		couriers:    make(map[string]*Courier),
		poolHasPeer: poolHasPeer,
		poolEvents:  poolEvents,
		poolSend:    poolSendFunc,
		ctx:         ctx,
		cancel:      cancel,
		cfg:         cfg,
	}

	if handler != nil {
		comp, ok := component.Get(ctx)
		if ok {
			pm.handler = handler
			pm.logger = slog.New(handler).With(slog.Any("component", comp))
		}
	}

	pm.wg.Add(1)
	go pm.handlePoolEvents()

	return pm
}

func (pm *Postmaster) Send(
	ctx context.Context,
	peerID string,
	data Envelope,
	callback func(LetterOutcome),
	opts ...SendOption,
) context.CancelFunc {
	cfg := sendConfig{deadline: pm.cfg.defaultDeadline}
	for _, opt := range opts {
		opt(&cfg)
	}

	pm.mu.RLock()
	defer pm.mu.RUnlock()

	// check if peer courier exists
	peerID, ok := pm.poolHasPeer(peerID)
	if !ok {
		go callback(LetterOutcome{PeerID: peerID, Kind: OutcomeRejected})
		return func() {}
	}
	courier, ok := pm.couriers[peerID]
	if !ok {
		go callback(LetterOutcome{PeerID: peerID, Kind: OutcomeRejected})
		return func() {}
	}

	ctx, cancel := context.WithTimeout(ctx, cfg.deadline)
	letter := OutboundLetter{
		id:     pm.counter.Add(1),
		peerID: peerID,
		data:   data,
		ctx:    ctx,
		cancel: cancel,
	}

	courier.Enqueue(letter, callback)

	return cancel
}

func (pm *Postmaster) Peers() []string {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	peers := make([]string, 0, len(pm.couriers))
	for id, _ := range pm.couriers {
		peers = append(peers, id)
	}
	return peers
}

func (pm *Postmaster) Close() {
	pm.cancel()
	pm.wg.Wait()

	// close each courier
	pm.mu.Lock()
	couriers := pm.couriers
	pm.couriers = make(map[string]*Courier)
	pm.mu.Unlock()

	for _, courier := range couriers {
		courier.Close()
	}
}

func (pm *Postmaster) handlePoolEvents() {
	defer pm.wg.Done()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case ev := <-pm.poolEvents:
			switch ev.Kind {
			case EventAdded:
				pm.mu.Lock()
				_, exists := pm.couriers[ev.ID]
				if exists {
					pm.mu.Unlock()
					continue
				}
				send := func(data Envelope) error { return pm.poolSend(ev.ID, data) }
				courier := NewCourier(pm.ctx, send, pm.handler)
				pm.couriers[ev.ID] = courier
				pm.mu.Unlock()

			case EventRemoved:
				pm.mu.Lock()
				courier, exists := pm.couriers[ev.ID]
				if exists {
					delete(pm.couriers, ev.ID)
				}
				pm.mu.Unlock()
				courier.Close()

			case EventConnected:
				pm.mu.RLock()
				courier, exists := pm.couriers[ev.ID]
				if exists {
					courier.HandleConnect()
				}
				pm.mu.RUnlock()

			case EventDisconnected:
				pm.mu.RLock()
				courier, exists := pm.couriers[ev.ID]
				if exists {
					courier.HandleDisconnect()
				}
				pm.mu.RUnlock()
			}
		}
	}
}

// ----------------------------------------------------------------------------
// Courier
// ----------------------------------------------------------------------------

// Letter State

type letterState struct {
	letter    OutboundLetter
	onOutcome func(LetterOutcome)

	sentAt   time.Time
	missedAt time.Time
	retries  int
	once     sync.Once
}

func (s *letterState) isCancelled() bool {
	return s.letter.ctx.Err() != nil
}

func (s *letterState) countRetry()              { s.retries++ }
func (s *letterState) setSentAt(at time.Time)   { s.sentAt = at }
func (s *letterState) setMissedAt(at time.Time) { s.missedAt = at }

// Courier

func NewCourier(
	ctx context.Context,
	sendFunc func(data Envelope) error, // func => PoolSendFunc(id)
	handler slog.Handler,
) *Courier {
	ctx, cancel := context.WithCancel(
		component.MustExtend(ctx, "courier"))

	c := &Courier{
		tasks:    make(chan courierTask, 64),
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
	wrappedLetter := &letterState{
		letter:    letter,
		onOutcome: onOutcome,
	}
	c.order(taskEnqueue{letter: wrappedLetter})
}

func (c *Courier) HandleConnect() {
	c.order(taskConnected{})
}

func (c *Courier) HandleDisconnect() {
	c.order(taskDisconnected{})
}

func (c *Courier) Close() {
	c.cancel()
	c.wg.Wait()
	c.terminate()
}

// Internal

func (c *Courier) order(task courierTask) {
	select {
	case <-c.ctx.Done():
	case c.tasks <- task:
	}
}

func (c *Courier) run() {
	defer c.wg.Done()

	for {
		select {
		case <-c.ctx.Done():
			return
		case task := <-c.tasks:
			task.dispatch(c)
			c.maybeSend()
		}
	}
}

func (c *Courier) maybeSend() {
	if !c.preflight() {
		c.drain()
		return
	}

	s, ok := c.pop()
	if !ok {
		return
	}
	c.sending = true

	c.wg.Add(1)
	go c.sendOnce(s)

}

func (c *Courier) sendOnce(s *letterState) {
	defer c.wg.Done()
	err := c.sendFunc(s.letter.data)
	c.order(taskHandleSendResult{letter: s, at: time.Now(), err: err})
}

func (c *Courier) doneOnce(s *letterState) {
	var kind LetterOutcomeKind
	if s.isCancelled() {
		// letter was cancelled
		if s.letter.ctx.Err() == context.DeadlineExceeded {
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
		LetterID: s.letter.id,
		PeerID:   s.letter.peerID,
		Kind:     kind,
		SentAt:   s.sentAt,
		MissedAt: s.missedAt,
		Retries:  s.retries,
	}

	s.once.Do(func() {
		s.letter.cancel()
		go s.onOutcome(outcome)
	})
}

func (c *Courier) terminate() {
	// cancel remaining letters
	for {
		s, ok := c.pop()
		if !ok {
			break
		}
		s.letter.cancel()
		s.setMissedAt(time.Now())
		c.doneOnce(s)
	}
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

		s := front.Value.(*letterState)
		if !s.isCancelled() {
			return
		}

		s.setMissedAt(time.Now())
		c.doneOnce(s)
		c.queue.Remove(front)
	}
}

func (c *Courier) pop() (*letterState, bool) {
	for {
		front := c.queue.Front()
		if front == nil {
			return nil, false
		}

		s := front.Value.(*letterState)
		c.queue.Remove(front)

		if !s.isCancelled() {
			return s, true
		}

		s.setMissedAt(time.Now())
		c.doneOnce(s)
	}
}

// ----------------------------------------------------------------------------
// Courier Messages
// ----------------------------------------------------------------------------

type taskEnqueue struct{ letter *letterState }

func (t taskEnqueue) dispatch(c *Courier) {
	c.queue.PushBack(t.letter)
}

type taskConnected struct{}

func (t taskConnected) dispatch(c *Courier) {
	c.connected = true
}

type taskDisconnected struct{}

func (t taskDisconnected) dispatch(c *Courier) {
	c.connected = false
}

type taskHandleSendResult struct {
	letter *letterState
	at     time.Time
	err    error
}

func (t taskHandleSendResult) dispatch(c *Courier) {
	c.sending = false
	if t.err != nil {
		t.letter.countRetry()
		c.queue.PushFront(t.letter)
	} else {
		t.letter.setSentAt(t.at)
		c.doneOnce(t.letter)
	}
}
