package relay

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadOrCreateKeypairKeepsIdentity(t *testing.T) {
	dir := t.TempDir()
	kp, err := LoadOrCreateKeypair(dir)
	if err != nil {
		t.Fatal(err)
	}
	again, err := LoadOrCreateKeypair(dir)
	if err != nil || again != kp {
		t.Fatalf("reload = %v, %v; want the saved keypair", again, err)
	}

	path := filepath.Join(dir, keypairFileName)
	other, _ := GenerateKeypair()
	for name, content := range map[string]string{
		"corrupt":    "{not json",
		"mismatched": `{"private":"` + EncodeKey(kp.Private) + `","public":"` + EncodeKey(other.Public) + `"}`,
	} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadOrCreateKeypair(dir); err == nil {
			t.Fatalf("%s keypair file was accepted", name)
		}
		if raw, _ := os.ReadFile(path); string(raw) != content {
			t.Fatalf("%s keypair file was replaced", name)
		}
	}
}
