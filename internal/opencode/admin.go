package opencode

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"wada-desktop-go/internal/agent"
)

func PairLink(raw string) (addr, code string, ok bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", "", false
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", "", false
	}
	path := strings.TrimSuffix(u.Path, "/")
	at := strings.LastIndex(path, "/auth/connect/")
	if at < 0 {
		return "", "", false
	}
	prefix, code := path[:at], path[at+len("/auth/connect/"):]
	if code == "" || strings.Contains(code, "/") {
		return "", "", false
	}
	return u.Scheme + "://" + u.Host + prefix, code, true
}

func RedeemPairLink(ctx context.Context, link string) (string, string, error) {
	addr, code, ok := PairLink(link)
	if !ok {
		return "", "", fmt.Errorf("that is not a pairing link")
	}
	base, err := APIBase(addr)
	if err != nil {
		return "", "", err
	}
	req, err := http.NewRequestWithContext(ctx, "GET",
		strings.TrimSuffix(base, "/api")+"/auth/connect/"+url.PathEscape(code), nil)
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := HTTPClient().Do(req)
	if err != nil {
		return "", "", err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		var tagged struct {
			Message string `json:"message"`
		}
		if json.Unmarshal(raw, &tagged) == nil && tagged.Message != "" {
			return "", "", fmt.Errorf("%s", tagged.Message)
		}
		return "", "", fmt.Errorf("%s: %s", addr, resp.Status)
	}
	var out struct {
		Token string `json:"token"`
		Data  struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", "", fmt.Errorf("%s did not read as a pairing answer", addr)
	}
	if out.Token == "" {
		out.Token = out.Data.Token
	}
	if out.Token == "" {
		return "", "", fmt.Errorf("%s handed back no token", addr)
	}
	return addr, out.Token, nil
}

func MintPairLink(ctx context.Context, cfg agent.Config, facts agent.Facts) (string, error) {
	ag, err := NewClient(cfg)
	if err != nil {
		return "", err
	}
	raw, err := ag.Call(ctx, "POST", "/pair", struct{}{})
	if err != nil {
		return "", err
	}
	var out struct {
		Code string `json:"code"`
		Data struct {
			Code string `json:"code"`
		} `json:"data"`
	}
	if json.Unmarshal(raw, &out) != nil {
		return "", fmt.Errorf("the pairing answer did not read")
	}
	code := out.Code
	if code == "" {
		code = out.Data.Code
	}
	if code == "" {
		return "", fmt.Errorf("the server minted no pairing code")
	}
	addr := strings.TrimSuffix(cfg.URL, "/")
	if len(facts.URLs) > 0 {
		addr = strings.TrimSuffix(facts.URLs[0], "/")
	}
	if addr == "" {
		addr = ag.Address
	}
	return addr + "/auth/connect/" + url.PathEscape(code), nil
}

func ProbeServer(ctx context.Context, cfg agent.Config) (agent.Facts, error) {
	base, err := APIBase(cfg.URL)
	if err != nil {
		return agent.Facts{}, err
	}
	req, err := http.NewRequestWithContext(ctx, "GET", base+"/info", nil)
	if err != nil {
		return agent.Facts{}, err
	}
	req.SetBasicAuth("opencode", cfg.Password)
	resp, err := HTTPClient().Do(req)
	if err != nil {
		return agent.Facts{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return agent.Facts{}, fmt.Errorf("%s", RefusedLine(cfg.Paired))
	}
	if resp.StatusCode >= 300 {
		return agent.Facts{}, fmt.Errorf("%s: %s", base, resp.Status)
	}
	if facts := ReadInfo(raw); facts.Version != "" {
		return facts, nil
	}
	return agent.Facts{}, fmt.Errorf("%s did not name its version", base)
}
