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

type Envelope = []byte
type PoolSendFunc = func(id string, data Envelope) error

// Pool Plugins

type EmbassyPlugin struct {
	Connect func(id string) error
	Remove  func(id string) error
	Send    PoolSendFunc
	Events  <-chan honeybee.OutboundPoolEvent
}

type HotelPlugin struct {
	Add     func(id string, socket honeybee.Socket) error
	Replace func(id string, socket honeybee.Socket) error
	Remove  func(id string) error
	Send    PoolSendFunc
	Events  <-chan honeybee.InboundPoolEvent
}

// Events

type PoolEventKind = int

const (
	EventConnected PoolEventKind = iota
	EventDisconnected
	EventRemoved
)

type PoolEvent struct {
	ID   string
	Kind PoolEventKind
	At   time.Time
}

// Adapter

type Adapter interface {
	Peers() []string
	HasPeer(id string) bool
	IsConnected(id string) bool
	Subscribe() <-chan PoolEvent
	Send(id string, data Envelope) error
}

// Embassy

type Embassy struct {
	pool      EmbassyPlugin
	peers     map[string]bool // peerID: isConnected
	journals  chan JournalEntry
	eventSubs []chan PoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

// Hotel

type Hotel struct {
	pool      HotelPlugin
	peers     map[string]bool // peerID: isConnected
	journals  chan JournalEntry
	eventSubs []chan PoolEvent

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.RWMutex
	wg     sync.WaitGroup
	logger *slog.Logger
}

// ----------------------------------------------------------------------------
// Embassy (Outbound Adapter)
// ----------------------------------------------------------------------------

func NewEmbassy() *Embassy {
	return nil
}

func (e *Embassy) Dispatch(url string) error {
	return nil
}

func (e *Embassy) Dismiss(url string) error {
	return nil
}

func (e *Embassy) Close() {}

func (e *Embassy) Peers() []string {
	return nil
}

func (e *Embassy) HasPeer(id string) bool {
	return false
}

func (e *Embassy) IsConnected(id string) bool {
	return false
}

func (e *Embassy) Subscribe() <-chan PoolEvent {
	return nil
}

func (e *Embassy) Send(id string, data Envelope) error {
	return nil
}

// ----------------------------------------------------------------------------
// Hotel (Inbound Adapter)
// ----------------------------------------------------------------------------

func NewHotel() *Hotel {
	return nil
}

func (h *Hotel) Welcome(id string, socket honeybee.Socket) error {
	return nil
}

func (h *Hotel) WelcomeBack(id string, socket honeybee.Socket) error {
	return nil
}

func (h *Hotel) Farewell(id string) error {
	return nil
}

func (h *Hotel) Close() {}

func (h *Hotel) Peers() []string {
	return nil
}

func (h *Hotel) HasPeer(id string) bool {
	return false
}

func (h *Hotel) IsConnected(id string) bool {
	return false
}

func (h *Hotel) Subscribe() <-chan PoolEvent {
	return nil
}

func (h *Hotel) Send(id string, data Envelope) error {
	return nil
}
