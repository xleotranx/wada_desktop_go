package client

import (
	"os"
	"path/filepath"
	"testing"

	"wada-desktop-go/internal/relay"
)

func TestHostsSaveLoadRemove(t *testing.T) {
	dir := t.TempDir()
	kp, err := relay.GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	link, err := relay.OfferURL("https://app.wada.dev", relay.Offer{
		Version:      relay.OfferVersion,
		RelayURL:     "https://relay.wada.dev",
		ServerID:     "srv_home",
		ServerPubKey: relay.EncodeKey(kp.Public),
		Token:        "tok_home",
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := SaveHost(dir, HostRecord{Name: "home", Offer: link}); err != nil {
		t.Fatal(err)
	}
	if err := SaveHost(dir, HostRecord{Name: "gpu", Endpoint: "100.64.0.4:6767", Credential: "tok_gpu"}); err != nil {
		t.Fatal(err)
	}

	list, err := LoadHosts(dir)
	if err != nil || len(list) != 2 {
		t.Fatalf("LoadHosts = %+v, err=%v", list, err)
	}
	if list[0].ServerID != "srv_home" || list[0].Credential != "tok_home" {
		t.Fatalf("unexpected decoded host[0]: %+v", list[0])
	}

	info, err := os.Stat(filepath.Join(dir, hostsFileName))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("hosts file perm = %v, err=%v", info.Mode().Perm(), err)
	}

	if err := RemoveHost(dir, "gpu"); err != nil {
		t.Fatal(err)
	}
	list, err = LoadHosts(dir)
	if err != nil || len(list) != 1 || list[0].Name != "home" {
		t.Fatalf("after remove LoadHosts = %+v, err=%v", list, err)
	}
}

func TestParseHostTarget(t *testing.T) {
	for target, want := range map[string][2]string{
		"my-mac.tailnet.ts.net":         {"", "my-mac.tailnet.ts.net:6767"},
		"100.64.0.4:7000":               {"", "100.64.0.4:7000"},
		"[fd7a::1]":                     {"", "[fd7a::1]:6767"},
		"ws://box:6767/ws":              {"", "ws://box:6767/ws"},
		"https://wada.example/#offer=x": {"https://wada.example/#offer=x", ""},
	} {
		offer, endpoint := ParseHostTarget(target)
		if offer != want[0] || endpoint != want[1] {
			t.Errorf("ParseHostTarget(%q) = %q, %q; want %q, %q", target, offer, endpoint, want[0], want[1])
		}
	}
}
