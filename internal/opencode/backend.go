package opencode

import (
	"context"
	"time"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/workspace"
)

type Backend struct{}

func (Backend) ID() string { return "opencode" }
func (Backend) Start(ctx context.Context, cfg agent.Config, dir string) (agent.Client, error) {
	return StartClient(ctx, cfg, dir)
}
func (Backend) Client(cfg agent.Config) (agent.Client, error) {
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}
func (Backend) Probe(ctx context.Context, cfg agent.Config) (agent.Facts, error) {
	return ProbeServer(ctx, cfg)
}
func (Backend) ValidateAddress(address string) (string, error)   { return APIBase(address) }
func (Backend) ParsePairLink(link string) (string, string, bool) { return PairLink(link) }
func (Backend) RedeemPairLink(ctx context.Context, link string) (string, string, error) {
	return RedeemPairLink(ctx, link)
}
func (Backend) MintPairLink(ctx context.Context, cfg agent.Config, facts agent.Facts) (string, error) {
	return MintPairLink(ctx, cfg, facts)
}

var _ agent.Backend = Backend{}

func (Backend) Policy(cfg agent.Config) (workspace.Policy, error) {
	if cfg.Local {
		return &Client{Local: true}, nil
	}
	client, err := NewClient(cfg)
	if err != nil {
		return nil, err
	}
	return client, nil
}

func (Backend) PairingPolicy() agent.PairingPolicy {
	return agent.PairingPolicy{TokenLifetime: 30 * 24 * time.Hour, LinkLifetime: 5 * time.Minute}
}
