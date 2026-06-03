package prism

import (
	"context"
	"sync"
	"time"
)

type NoticeReceived struct {
	Message string
	At      time.Time
}

type Notice struct {
	PeerID    string
	Message   string
	Timestamp time.Time
}

type NoticeHandler struct {
	envoy   *Envoy
	inbox   <-chan InboxMessage
	notices chan Notice

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func NewNoticeHandler(e *Envoy) *NoticeHandler {
	// SubscribeInbox([]string{"NOTICE"})
	// make(chan Notice, 16)
	// context.WithCancel from e.Context()
	// wg.Add(1); go h.route()
	return nil
}

func (h *NoticeHandler) Notices() <-chan Notice { return h.notices }

func (h *NoticeHandler) Close() {
	// h.cancel()
	// h.wg.Wait()
	// close(h.notices)
}

func (h *NoticeHandler) route() {
	defer h.wg.Done()
	// select ctx.Done | inbox msg
	//   msg: envelope.FindNotice → on err continue
	//         push Notice{PeerID, Message, time.Now()} to h.notices (blocking)
	//         e.Observer().Record(e.PeerID(), NoticeReceived{...})
}
