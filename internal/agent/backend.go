package agent

import (
	"context"
	"time"
)

// Config describes a saved connection. Each backend validates and interprets it;
// the native shell never constructs its protocol client or launches its process.
type Config struct {
	URL, Password          string
	Paired, Managed, Local bool
}
type Backend interface {
	ID() string
	Start(context.Context, Config, string) (Client, error)
	// Client constructs a lightweight handle without network or process I/O.
	Client(Config) (Client, error)
	Probe(context.Context, Config) (Facts, error)
	ValidateAddress(string) (string, error)
}
type PairingPolicy struct{ TokenLifetime, LinkLifetime time.Duration }
type Pairing interface {
	PairingPolicy() PairingPolicy
	ParsePairLink(string) (string, string, bool)
	RedeemPairLink(context.Context, string) (string, string, error)
	MintPairLink(context.Context, Config, Facts) (string, error)
}
