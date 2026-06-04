package prism

import (
	"context"
	"git.wisehodl.dev/jay/go-mana-component"
	"log/slog"
	"sync"
	"time"

	"git.wisehodl.dev/jay/go-roots-ws"
)

type ChallengeReceived struct {
	Challenge string
	At        time.Time
}

type AuthResponseSent struct {
	At time.Time
}

type AuthResponseFailed struct {
	Err error
	At  time.Time
}

type AuthManager struct {
	envoy     *Envoy
	respond   func(challenge string) ([]byte, error)
	challenge string

	inbox  <-chan InboxMessage
	events <-chan OutboundPoolEvent

	ctx     context.Context
	cancel  context.CancelFunc
	mu      sync.Mutex
	wg      sync.WaitGroup
	handler slog.Handler
	logger  *slog.Logger
}

func NewAuthManager(e *Envoy, respond func(string) ([]byte, error)) *AuthManager {
	ctx, cancel := context.WithCancel(
		component.MustExtend(e.Context(), "auth_manager"))

	m := &AuthManager{
		envoy:   e,
		respond: respond,
		inbox:   e.SubscribeInbox([]string{"AUTH"}),
		events:  e.SubscribeEvents(),
		ctx:     ctx,
		cancel:  cancel,
	}

	if e.Handler() != nil {
		comp := component.FromContext(ctx)
		m.handler = e.Handler()
		m.logger = slog.New(m.handler).With(slog.Any("component", comp))
	}

	m.wg.Go(m.routeInbox)
	m.wg.Go(m.handleEvents)
	return m
}

func (m *AuthManager) Close() {
	m.cancel()
	m.wg.Wait()
}

func (m *AuthManager) routeInbox() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case msg := <-m.inbox:
			challenge, err := envelope.FindAuthChallenge(msg.Data)
			if err != nil {
				continue
			}

			m.mu.Lock()
			m.challenge = challenge
			m.mu.Unlock()

			now := time.Now()
			m.envoy.Observer().Record(msg.ID, ChallengeReceived{Challenge: challenge, At: now})

			signed, err := m.respond(challenge)
			if err != nil {
				m.envoy.Observer().Record(msg.ID, AuthResponseFailed{Err: err, At: time.Now()})
				continue
			}

			if err := m.envoy.Send([]byte(envelope.EncloseAuthResponse(signed))); err != nil {
				m.envoy.Observer().Record(msg.ID, AuthResponseFailed{Err: err, At: time.Now()})
				continue
			}

			if m.logger != nil {
				m.logger.Info("responded to auth", "challenge", challenge)
			}

			m.envoy.Observer().Record(msg.ID, AuthResponseSent{At: time.Now()})
		}
	}
}

func (m *AuthManager) handleEvents() {
	for {
		select {
		case <-m.ctx.Done():
			return
		case ev := <-m.events:
			if ev.Kind == EventDisconnected {
				m.mu.Lock()
				m.challenge = ""
				m.mu.Unlock()
			}
		}
	}
}
