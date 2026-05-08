package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-honeybee"
	"log/slog"
	"sync"
	"time"
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

func NewClerk() *Clerk {
	return nil
}

func (c *Clerk) Subscribe() {}

func (c *Clerk) Start() {}

func (c *Clerk) Close() {}
