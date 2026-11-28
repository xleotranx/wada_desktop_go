package daemon

import (
	"context"

	"wada-desktop-go/internal/contract/browser"
	"wada-desktop-go/internal/protocol"
)

func (p *Plugin) PendingPreview(ctx context.Context, workspace string) (*browser.Request, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	r, err := cli.PendingPreview(ctx, workspace)
	return (*browser.Request)(r), err
}

func (p *Plugin) ClaimPreview(ctx context.Context, id string, allow bool) (*browser.Request, error) {
	cli, err := p.current()
	if err != nil {
		return nil, err
	}
	r, err := cli.ClaimPreview(ctx, id, allow)
	return (*browser.Request)(r), err
}

func (p *Plugin) ReplyPreview(ctx context.Context, reply browser.Reply) error {
	cli, err := p.current()
	if err != nil {
		return err
	}
	return cli.ReplyPreview(ctx, protocol.PreviewReply(reply))
}
