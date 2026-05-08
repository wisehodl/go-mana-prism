package prism

import (
	"container/list"
	"context"
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
}

type letterRecord struct {
}

type LetterOutcomeKind int

type LetterOutcome struct {
}

// Postmaster

type Postmaster struct {
	couriers map[string]*Courier
	letters  map[LetterID]letterRecord
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
	id     string // peer id
	master *Postmaster
	cmd    chan courierCommand
	send   func(data Envelope) error

	// state
	queue     list.List
	connected bool
	sending   bool

	ctx    context.Context
	cancel context.CancelFunc
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

func NewCourier() *Courier {
	return nil
}

func (c *Courier) Enqueue(letter OutboundLetter) {}

func (c *Courier) Close() {}

// ----------------------------------------------------------------------------
// Commands
// ----------------------------------------------------------------------------
