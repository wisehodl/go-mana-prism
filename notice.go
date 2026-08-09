package prism

import (
	"context"
	"sync"
	"time"

	"git.wisehodl.dev/jay/go-roots-ws"
)

type NoticeReceived struct {
	Message string
	At      time.Time
}

type Notice struct {
	Peer      string
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
	ctx, cancel := context.WithCancel(e.Context())
	h := &NoticeHandler{
		envoy:   e,
		inbox:   e.SubscribeInbox([]string{"NOTICE"}),
		notices: make(chan Notice, 16),
		ctx:     ctx,
		cancel:  cancel,
	}
	h.wg.Go(h.route)
	return h
}

func (h *NoticeHandler) Notices() <-chan Notice { return h.notices }

func (h *NoticeHandler) Close() {
	h.cancel()
	h.wg.Wait()
	close(h.notices)
}

func (h *NoticeHandler) route() {
	for {
		select {
		case <-h.ctx.Done():
			return
		case msg, ok := <-h.inbox:
			if !ok {
				return
			}
			message, err := envelope.FindNotice(msg.Data)
			if err != nil {
				continue
			}
			now := time.Now()
			h.envoy.Observer().Record(msg.URL, NoticeReceived{
				Message: message,
				At:      now,
			})
			h.notices <- Notice{
				Peer:      msg.URL,
				Message:   message,
				Timestamp: now,
			}
		}
	}
}
