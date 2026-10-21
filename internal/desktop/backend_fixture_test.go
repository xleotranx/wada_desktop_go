package desktop

import (
	"context"
	"os"
	"strings"

	"wada-desktop-go/internal/agent"
	"wada-desktop-go/internal/opencode"
)

func pairLink(link string) (string, string, bool) { return opencode.PairLink(link) }
func redeemPairLink(ctx context.Context, link string) (string, string, error) {
	return opencode.RedeemPairLink(ctx, link)
}
func mintPairLink(ctx context.Context, cfg serverConn, facts agent.Facts) (string, error) {
	return opencode.MintPairLink(ctx, cfg.transport(), facts)
}
func probeServer(ctx context.Context, cfg serverConn) (agent.Facts, error) {
	return opencode.ProbeServer(ctx, cfg.transport())
}
func permissionLevel(dir string) (string, error)     { return opencode.PermissionLevel(dir) }
func shellGate(doc map[string]any) string            { return opencode.ShellGate(doc) }
func permissionRules(doc map[string]any) []any       { return opencode.PermissionRules(doc) }
func setShellGate(doc map[string]any, ask bool) bool { return opencode.SetShellGate(doc, ask) }
func setPermissionAsk(dir string, ask bool) error    { return opencode.SetPermissionAsk(dir, ask) }
func remoteSetPermissionAsk(ctx context.Context, cfg serverConn, dir string, ask bool) (string, error) {
	client, err := opencode.NewClient(cfg.transport())
	if err != nil {
		return "", err
	}
	return client.WritePolicy(ctx, dir, ask)
}

func ruleFields(raw any) (string, string, string, bool) { return opencode.RuleFields(raw) }
func v1BashStar(doc map[string]any) (string, bool)      { return opencode.V1BashStar(doc) }
func remotePermissionLevel(ctx context.Context, cfg serverConn, dir string) (string, error) {
	client, err := opencode.NewClient(cfg.transport())
	if err != nil {
		return "", err
	}
	return client.ReadPolicy(ctx, dir)
}

func fixtureDefaultModel() (string, string) {
	if value := os.Getenv("WADA_MODEL"); value != "" {
		provider, id, ok := strings.Cut(value, "/")
		if ok {
			return provider, id
		}
	}
	return "opencode-go", "longcat-2.5-preview-free"
}

func serverPasswordLine(s serverConn) string {
	return connectionPasswordLine(s, opencode.Backend{}.PairingPolicy().TokenLifetime)
}
