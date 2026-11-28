package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"wada-desktop-go/internal/relay"
)

const hostsFileName = "hosts.json"

// HostRecord stores a named remote wadad connection profile (direct or E2EE relay offer).
type HostRecord struct {
	Name       string `json:"name"`
	Offer      string `json:"offer,omitempty"`
	Endpoint   string `json:"endpoint,omitempty"`
	Credential string `json:"credential,omitempty"`
	RelayToken string `json:"relayToken,omitempty"` // Bearer token for the relay itself
	RelayURL   string `json:"relayUrl,omitempty"`
	ServerID   string `json:"serverId,omitempty"`
	UpdatedAt  int64  `json:"updatedAt"`
}

func resolveHostsDir(dataDir string) (string, error) {
	if dataDir != "" {
		return dataDir, nil
	}
	return DefaultDataDir()
}

// LoadHosts returns all saved remote hosts from <dataDir>/hosts.json.
func LoadHosts(dataDir string) ([]HostRecord, error) {
	dir, err := resolveHostsDir(dataDir)
	if err != nil {
		return nil, err
	}
	raw, err := os.ReadFile(filepath.Join(dir, hostsFileName))
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []HostRecord
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// SaveHost adds or updates a named remote host profile with 0600 permissions.
func SaveHost(dataDir string, rec HostRecord) error {
	if rec.Name == "" {
		return errors.New("client: host name is required")
	}
	if rec.Offer == "" && rec.Endpoint == "" {
		return errors.New("client: host requires offer or endpoint")
	}
	if rec.Offer != "" {
		off, err := relay.DecodeOffer(rec.Offer)
		if err != nil {
			return err
		}
		rec.RelayURL = off.RelayURL
		rec.Endpoint = off.DirectURL
		rec.ServerID = off.ServerID
		if rec.Credential == "" && off.Token != "" {
			rec.Credential = off.Token
		}
	}
	rec.UpdatedAt = time.Now().UnixMilli()

	dir, err := resolveHostsDir(dataDir)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	list, err := LoadHosts(dir)
	if err != nil {
		return err
	}
	if idx := slices.IndexFunc(list, func(h HostRecord) bool { return h.Name == rec.Name }); idx >= 0 {
		list[idx] = rec
	} else {
		list = append(list, rec)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, hostsFileName), data, 0o600)
}

// RemoveHost removes a named host profile from <dataDir>/hosts.json.
func RemoveHost(dataDir, name string) error {
	dir, err := resolveHostsDir(dataDir)
	if err != nil {
		return err
	}
	list, err := LoadHosts(dir)
	if err != nil {
		return err
	}
	filtered := slices.DeleteFunc(list, func(h HostRecord) bool { return h.Name == name })
	if len(filtered) == len(list) {
		return fmt.Errorf("client: host %q not found", name)
	}
	data, err := json.MarshalIndent(filtered, "", "  ")
	if err != nil {
		return err
	}
	return writeFileAtomic(filepath.Join(dir, hostsFileName), data, 0o600)
}

// DefaultPort is wadad's default listening port.
const DefaultPort = "6767"

// ParseHostTarget reads what a person typed to add a host: a pairing offer
// (an #offer= link or a bare encoded offer) or a daemon endpoint. An endpoint
// without a scheme or port gets DefaultPort.
func ParseHostTarget(target string) (offer, endpoint string) {
	target = strings.TrimSpace(target)
	if strings.Contains(target, "#offer=") {
		return target, ""
	}
	if _, err := relay.DecodeOffer(target); err == nil {
		return target, ""
	}
	if !strings.Contains(target, "://") {
		host := strings.TrimRight(target, "/")
		if _, _, err := net.SplitHostPort(host); err != nil {
			host = net.JoinHostPort(strings.Trim(host, "[]"), DefaultPort)
		}
		return "", host
	}
	return "", target
}

// DialHost connects to a saved remote host by name using its stored offer or endpoint.
func DialHost(ctx context.Context, dataDir, name string, opts Options) (*Client, error) {
	list, err := LoadHosts(dataDir)
	if err != nil {
		return nil, err
	}
	for _, h := range list {
		if h.Name == name {
			// A saved credential is a pairing token or the daemon's password,
			// and a password-protected daemon reads only the password field.
			if opts.Credential == "" && h.Credential != "" {
				opts.Credential = h.Credential
			}
			if opts.Password == "" {
				opts.Password = h.Credential
			}
			if opts.RelayAuthToken == "" {
				opts.RelayAuthToken = h.RelayToken
			}
			if h.Offer != "" {
				return DialOffer(ctx, h.Offer, opts)
			}
			opts.Endpoint = h.Endpoint
			return Dial(ctx, opts)
		}
	}
	return nil, fmt.Errorf("client: saved host %q not found", name)
}

func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if err = f.Chmod(mode); err == nil {
		if _, err = f.Write(data); err == nil {
			err = f.Sync()
		}
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	return os.Rename(tmp, path)
}
