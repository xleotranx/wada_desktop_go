package relay

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKeypairPersistenceAndHash(t *testing.T) {
	dir := t.TempDir()
	kp1, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	kp2, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	if kp1 != kp2 {
		t.Fatal("LoadOrCreateKeypair generated different key on second load")
	}
	info, err := os.Stat(filepath.Join(dir, keypairFileName))
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("keypair file mode = %o, want 0600", mode)
	}
	hash := ServerPubHash(kp1.Public)
	if !isValidServerHash(hash) {
		t.Fatalf("ServerPubHash = %q invalid", hash)
	}
}

func TestOfferEncodeDecodeURLVariants(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	want := Offer{
		Version:          OfferVersion,
		RelayURL:         "https://relay.wada.dev",
		DirectURL:        "http://100.64.0.9:6767",
		ServerID:         "srv_abc123",
		Hostname:         "mbp.local",
		ServerPubKey:     EncodeKey(kp.Public),
		Token:            "tok_secret",
		RequiresPassword: true,
	}

	link, err := OfferURL("https://app.wada.dev/", want)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(link, "https://app.wada.dev/#offer=") {
		t.Fatalf("unexpected link %q", link)
	}

	enc := strings.TrimPrefix(link, "https://app.wada.dev/#offer=")
	for _, input := range []string{link, "#offer=" + enc, enc} {
		got, err := DecodeOffer(input)
		if err != nil {
			t.Fatalf("DecodeOffer(%q): %v", input, err)
		}
		if got != want {
			t.Fatalf("DecodeOffer mismatch:\n got: %+v\nwant: %+v", got, want)
		}
	}

	for _, bad := range []string{"", "not-base64!!!", "https://app.wada.dev/#other=1"} {
		if _, err := DecodeOffer(bad); err == nil {
			t.Fatalf("expected error decoding %q", bad)
		}
	}
}
