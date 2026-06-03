package prism

import (
	"context"
	"sync"
	"time"
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

	ctx    context.Context
	cancel context.CancelFunc
	mu     sync.Mutex
	wg     sync.WaitGroup
}

func NewAuthManager(e *Envoy, respond func(string) ([]byte, error)) *AuthManager {
	// SubscribeInbox([]string{"AUTH"})
	// SubscribeEvents()
	// context.WithCancel from e.Context()
	// wg.Add(2); go m.routeInbox(); go m.handleEvents()
	return nil
}

func (m *AuthManager) Close() {
	// m.cancel(); m.wg.Wait()
}

func (m *AuthManager) routeInbox() {
	defer m.wg.Done()
	// select ctx.Done | inbox msg
	//   msg: envelope.FindAuthChallenge → on ErrWrongFieldType or err: continue
	//         store m.challenge with lock
	//         record ChallengeReceived
	//         call m.respond(challenge)
	//           on err: record AuthResponseFailed; continue
	//         envelope.EncloseAuthResponse(signed)
	//         m.envoy.Send(...)
	//           on err: record AuthResponseFailed; continue
	//         record AuthResponseSent
}

func (m *AuthManager) handleEvents() {
	defer m.wg.Done()
	// select ctx.Done | event
	//   EventDisconnected: m.challenge = "" with lock
}
