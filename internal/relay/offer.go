package relay

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
)

const OfferVersion = 1

// Offer carries everything a remote desktop or Web/PWA client needs to pair
// with a wadad instance over direct LAN/Tailscale or an E2EE wada-relay tunnel.
// It is designed to travel in a URL hash fragment (#offer=...) so relay servers
// and HTTP hosts never receive its secrets in server logs.
type Offer struct {
	Version          int    `json:"v"`
	RelayURL         string `json:"relayUrl,omitempty"`
	DirectURL        string `json:"directUrl,omitempty"`
	ServerID         string `json:"serverId"`
	Hostname         string `json:"hostname,omitempty"`
	ServerPubKey     string `json:"serverPubKey"`
	Token            string `json:"token,omitempty"`
	RequiresPassword bool   `json:"requiresPassword,omitempty"`
}

// EncodeOffer encodes an Offer as unpadded base64url JSON.
func EncodeOffer(o Offer) (string, error) {
	if o.Version == 0 {
		o.Version = OfferVersion
	}
	if o.ServerPubKey == "" {
		return "", errors.New("relay: offer missing serverPubKey")
	}
	if _, err := ParseKey(o.ServerPubKey); err != nil {
		return "", fmt.Errorf("relay: invalid serverPubKey: %w", err)
	}
	if o.RelayURL == "" && o.DirectURL == "" {
		return "", errors.New("relay: offer requires relayUrl or directUrl")
	}
	raw, err := json.Marshal(o)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}

// OfferURL builds a full pairing link embedding the offer in the URL fragment.
func OfferURL(appBaseURL string, o Offer) (string, error) {
	enc, err := EncodeOffer(o)
	if err != nil {
		return "", err
	}
	base := strings.TrimSpace(appBaseURL)
	if base == "" {
		base = "https://app.wada.dev"
	}
	base = strings.TrimRight(base, "/")
	return base + "/#offer=" + enc, nil
}

// DecodeOffer parses a full URL (#offer=...), bare fragment (#offer=...), or raw
// base64url offer token.
func DecodeOffer(raw string) (Offer, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return Offer{}, errors.New("relay: empty offer")
	}
	if idx := strings.Index(s, "#offer="); idx >= 0 {
		s = s[idx+len("#offer="):]
	} else if u, err := url.Parse(s); err == nil && u.Fragment != "" {
		if val, ok := strings.CutPrefix(u.Fragment, "offer="); ok {
			s = val
		}
	}
	if cut := strings.IndexByte(s, '&'); cut >= 0 {
		s = s[:cut]
	}
	data, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		data, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			return Offer{}, fmt.Errorf("relay: decode offer: %w", err)
		}
	}
	var o Offer
	if err := json.Unmarshal(data, &o); err != nil {
		return Offer{}, fmt.Errorf("relay: invalid offer json: %w", err)
	}
	if o.Version != OfferVersion {
		return Offer{}, fmt.Errorf("relay: unsupported offer version %d", o.Version)
	}
	if _, err := ParseKey(o.ServerPubKey); err != nil {
		return Offer{}, fmt.Errorf("relay: invalid offer key: %w", err)
	}
	if o.RelayURL == "" && o.DirectURL == "" {
		return Offer{}, errors.New("relay: offer has no connection endpoints")
	}
	return o, nil
}
