// Package preview coordinates agent requests and approvals in the local desktop.
package preview

import (
	"context"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/google/uuid"
	"wada-desktop-go/internal/protocol"
)

type pending struct {
	ctx   context.Context
	req   protocol.PreviewRequest
	owner string
	reply chan protocol.PreviewReply
}

type Broker struct {
	mu    sync.Mutex
	queue []*pending
}

func (b *Broker) Request(ctx context.Context, req protocol.PreviewRequest) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	req.ID = uuid.NewString()
	p := &pending{ctx: ctx, req: req, reply: make(chan protocol.PreviewReply, 1)}
	b.mu.Lock()
	if len(b.queue) >= 16 {
		b.mu.Unlock()
		return "", errors.New("too many pending browser requests")
	}
	b.queue = append(b.queue, p)
	b.mu.Unlock()
	defer func() {
		b.mu.Lock()
		b.queue = slices.DeleteFunc(b.queue, func(item *pending) bool { return item == p })
		b.mu.Unlock()
	}()
	select {
	case reply := <-p.reply:
		if reply.Error != "" {
			return "", errors.New(reply.Error)
		}
		return reply.Text, nil
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (b *Broker) Pending(workspace string) *protocol.PreviewRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.queue {
		if p.req.Workspace == workspace && p.owner == "" && p.ctx.Err() == nil {
			req := p.req
			return &req
		}
	}
	return nil
}

// Claim ensures that just one desktop can act on an approval.
func (b *Broker) Claim(id, owner string) *protocol.PreviewRequest {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.queue {
		if p.req.ID == id && p.owner == "" && p.ctx.Err() == nil {
			p.owner = owner
			req := p.req
			return &req
		}
	}
	return nil
}

func (b *Broker) Reply(result protocol.PreviewReply, owner string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	for _, p := range b.queue {
		if p.req.ID == result.ID && p.owner == owner {
			select {
			case p.reply <- result:
				return true
			default:
				return false
			}
		}
	}
	return false
}
