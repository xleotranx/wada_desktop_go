package relay

import (
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

const keypairFileName = "daemon-keypair.json"

// Keypair holds a daemon's static X25519 identity keypair for Noise NK.
type Keypair struct {
	Private [keySize]byte
	Public  [keySize]byte
}

type storedKeypair struct {
	Private string `json:"private"`
	Public  string `json:"public"`
}

// GenerateKeypair creates a fresh X25519 static keypair.
func GenerateKeypair() (Keypair, error) {
	priv, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return Keypair{}, err
	}
	var kp Keypair
	copy(kp.Private[:], priv.Bytes())
	copy(kp.Public[:], priv.PublicKey().Bytes())
	return kp, nil
}

// LoadOrCreateKeypair loads <dataDir>/daemon-keypair.json, or generates and
// atomically persists one with 0600 permissions when there is none. A file
// that cannot be read or parsed is an error, never replaced: the key is the
// daemon's identity, and a new one would break every pairing link.
func LoadOrCreateKeypair(dataDir string) (Keypair, error) {
	if dataDir == "" {
		return GenerateKeypair()
	}
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return Keypair{}, err
	}
	path := filepath.Join(dataDir, keypairFileName)
	raw, err := os.ReadFile(path)
	if err == nil {
		kp, perr := parseStoredKeypair(raw)
		if perr != nil {
			return Keypair{}, fmt.Errorf("relay: %s: %w; move it aside only if this daemon should get a new identity and every client should pair again", path, perr)
		}
		return kp, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return Keypair{}, err
	}

	kp, err := GenerateKeypair()
	if err != nil {
		return Keypair{}, err
	}
	disk := storedKeypair{
		Private: EncodeKey(kp.Private),
		Public:  EncodeKey(kp.Public),
	}
	raw, err = json.MarshalIndent(disk, "", "  ")
	if err != nil {
		return Keypair{}, err
	}
	if err := writeAtomicFile(path, raw, 0o600); err != nil {
		return Keypair{}, err
	}
	return kp, nil
}

// parseStoredKeypair reads daemon-keypair.json and checks that its public key
// is the private key's.
func parseStoredKeypair(raw []byte) (Keypair, error) {
	var disk storedKeypair
	if err := json.Unmarshal(raw, &disk); err != nil {
		return Keypair{}, err
	}
	priv, err := ParseKey(disk.Private)
	if err != nil {
		return Keypair{}, fmt.Errorf("private key: %w", err)
	}
	key, err := ecdh.X25519().NewPrivateKey(priv[:])
	if err != nil {
		return Keypair{}, fmt.Errorf("private key: %w", err)
	}
	kp := Keypair{Private: priv}
	copy(kp.Public[:], key.PublicKey().Bytes())
	if disk.Public != "" {
		pub, err := ParseKey(disk.Public)
		if err != nil || pub != kp.Public {
			return Keypair{}, errors.New("public key does not match the private key")
		}
	}
	return kp, nil
}

// EncodeKey encodes a 32-byte X25519 key in unpadded base64url.
func EncodeKey(k [keySize]byte) string {
	return base64.RawURLEncoding.EncodeToString(k[:])
}

// ParseKey decodes an unpadded (or padded) base64url/base64 32-byte key.
func ParseKey(s string) ([keySize]byte, error) {
	var out [keySize]byte
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(s)
		if err != nil {
			raw, err = base64.StdEncoding.DecodeString(s)
			if err != nil {
				return out, err
			}
		}
	}
	if len(raw) != keySize {
		return out, errors.New("relay: invalid 32-byte key length")
	}
	copy(out[:], raw)
	return out, nil
}

// ServerPubHash returns the 32-char lowercase hex identifier (first 16 bytes of
// SHA-256(pub)) used by wada-relay to route traffic without seeing plaintext.
func ServerPubHash(pub [keySize]byte) string {
	sum := sha256.Sum256(pub[:])
	return hex.EncodeToString(sum[:16])
}

func isValidServerHash(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, r := range s {
		if (r < '0' || r > '9') && (r < 'a' || r > 'f') {
			return false
		}
	}
	return true
}

func writeAtomicFile(path string, data []byte, mode os.FileMode) error {
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
