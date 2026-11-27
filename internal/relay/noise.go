// Package relay implements zero-knowledge end-to-end encrypted tunneling
// (Noise_NK_25519_ChaChaPoly_SHA256), pairing offers, and stateless WebSocket routing.
package relay

import (
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"golang.org/x/crypto/chacha20poly1305"
)

const (
	// ProtocolName is the exact 32-byte Noise protocol identifier.
	ProtocolName = "Noise_NK_25519_ChaChaPoly_SHA256"
	keySize      = 32
)

var (
	errNonceExhausted = errors.New("relay: noise nonce exhausted")
	errShortMessage   = errors.New("relay: handshake message too short")
)

// CipherState maintains a 32-byte key and strictly monotonic 64-bit nonce.
type CipherState struct {
	key    [keySize]byte
	hasKey bool
	nonce  uint64
	aead   cipher.AEAD
}

func (cs *CipherState) initializeKey(k *[keySize]byte) error {
	if k == nil {
		cs.hasKey = false
		cs.nonce = 0
		cs.aead = nil
		cs.key = [keySize]byte{}
		return nil
	}
	aead, err := chacha20poly1305.New(k[:])
	if err != nil {
		return err
	}
	cs.key = *k
	cs.hasKey = true
	cs.nonce = 0
	cs.aead = aead
	return nil
}

func makeNoiseNonce(n uint64) [chacha20poly1305.NonceSize]byte {
	var out [chacha20poly1305.NonceSize]byte
	binary.LittleEndian.PutUint64(out[4:], n)
	return out
}

// EncryptWithAD encrypts plaintext with associated data ad and advances the monotonic nonce.
func (cs *CipherState) EncryptWithAD(ad, plaintext []byte) ([]byte, error) {
	if !cs.hasKey {
		out := make([]byte, len(plaintext))
		copy(out, plaintext)
		return out, nil
	}
	if cs.nonce == math.MaxUint64 {
		return nil, errNonceExhausted
	}
	nonce := makeNoiseNonce(cs.nonce)
	out := cs.aead.Seal(nil, nonce[:], plaintext, ad)
	cs.nonce++
	return out, nil
}

// DecryptWithAD authenticates and decrypts ciphertext with ad and advances the nonce on success.
func (cs *CipherState) DecryptWithAD(ad, ciphertext []byte) ([]byte, error) {
	if !cs.hasKey {
		out := make([]byte, len(ciphertext))
		copy(out, ciphertext)
		return out, nil
	}
	if cs.nonce == math.MaxUint64 {
		return nil, errNonceExhausted
	}
	nonce := makeNoiseNonce(cs.nonce)
	out, err := cs.aead.Open(nil, nonce[:], ciphertext, ad)
	if err != nil {
		return nil, err
	}
	cs.nonce++
	return out, nil
}

type symmetricState struct {
	cs CipherState
	ck [keySize]byte
	h  [keySize]byte
}

func newSymmetricState() *symmetricState {
	var ss symmetricState
	copy(ss.h[:], ProtocolName)
	ss.ck = ss.h
	return &ss
}

func (ss *symmetricState) mixHash(data []byte) {
	h := sha256.New()
	h.Write(ss.h[:])
	h.Write(data)
	copy(ss.h[:], h.Sum(nil))
}

func hmacSHA256(key, data []byte) [keySize]byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	var out [keySize]byte
	copy(out[:], mac.Sum(nil))
	return out
}

func hkdf2(ck [keySize]byte, ikm []byte) (t1, t2 [keySize]byte) {
	tempKey := hmacSHA256(ck[:], ikm)
	t1 = hmacSHA256(tempKey[:], []byte{0x01})
	var in2 [keySize + 1]byte
	copy(in2[:], t1[:])
	in2[keySize] = 0x02
	t2 = hmacSHA256(tempKey[:], in2[:])
	return t1, t2
}

func (ss *symmetricState) mixKey(ikm []byte) error {
	ck, tempK := hkdf2(ss.ck, ikm)
	ss.ck = ck
	return ss.cs.initializeKey(&tempK)
}

func (ss *symmetricState) encryptAndHash(plaintext []byte) ([]byte, error) {
	ct, err := ss.cs.EncryptWithAD(ss.h[:], plaintext)
	if err != nil {
		return nil, err
	}
	ss.mixHash(ct)
	return ct, nil
}

func (ss *symmetricState) decryptAndHash(ciphertext []byte) ([]byte, error) {
	pt, err := ss.cs.DecryptWithAD(ss.h[:], ciphertext)
	if err != nil {
		return nil, err
	}
	ss.mixHash(ciphertext)
	return pt, nil
}

func (ss *symmetricState) split() (*CipherState, *CipherState, [keySize]byte, error) {
	k1, k2 := hkdf2(ss.ck, nil)
	c1 := &CipherState{}
	c2 := &CipherState{}
	if err := c1.initializeKey(&k1); err != nil {
		return nil, nil, [keySize]byte{}, err
	}
	if err := c2.initializeKey(&k2); err != nil {
		return nil, nil, [keySize]byte{}, err
	}
	return c1, c2, ss.h, nil
}

// HandshakeState implements one side of Noise_NK_25519_ChaChaPoly_SHA256.
type HandshakeState struct {
	ss        *symmetricState
	initiator bool
	rs        *ecdh.PublicKey
	s         *ecdh.PrivateKey
	e         *ecdh.PrivateKey
	re        *ecdh.PublicKey
	msgIndex  int

	// genEphemeral overrides random key generation in deterministic vector tests.
	genEphemeral func() (*ecdh.PrivateKey, error)
}

// NewInitiator initializes a Noise NK initiator with responder's static public key.
func NewInitiator(responderStaticPub [keySize]byte, prologue []byte) (*HandshakeState, error) {
	rs, err := ecdh.X25519().NewPublicKey(responderStaticPub[:])
	if err != nil {
		return nil, fmt.Errorf("relay: invalid responder static key: %w", err)
	}
	ss := newSymmetricState()
	ss.mixHash(prologue)
	ss.mixHash(rs.Bytes())
	return &HandshakeState{
		ss:        ss,
		initiator: true,
		rs:        rs,
	}, nil
}

// NewResponder initializes a Noise NK responder with its static private key.
func NewResponder(staticPriv [keySize]byte, prologue []byte) (*HandshakeState, error) {
	s, err := ecdh.X25519().NewPrivateKey(staticPriv[:])
	if err != nil {
		return nil, fmt.Errorf("relay: invalid static private key: %w", err)
	}
	ss := newSymmetricState()
	ss.mixHash(prologue)
	ss.mixHash(s.PublicKey().Bytes())
	return &HandshakeState{
		ss:        ss,
		initiator: false,
		s:         s,
	}, nil
}

func (hs *HandshakeState) nextEphemeral() (*ecdh.PrivateKey, error) {
	if hs.genEphemeral != nil {
		return hs.genEphemeral()
	}
	return ecdh.X25519().GenerateKey(rand.Reader)
}

// WriteMessage writes the next NK handshake message (msg 1 for initiator, msg 2 for responder).
// When the final handshake message completes, send/recv CipherStates and handshake hash h are non-nil.
func (hs *HandshakeState) WriteMessage(payload []byte) (msg []byte, send, recv *CipherState, h [keySize]byte, err error) {
	switch {
	case hs.initiator && hs.msgIndex == 0:
		// -> e, es
		e, gerr := hs.nextEphemeral()
		if gerr != nil {
			return nil, nil, nil, h, gerr
		}
		hs.e = e
		ePub := e.PublicKey().Bytes()
		hs.ss.mixHash(ePub)

		shared, derr := e.ECDH(hs.rs)
		if derr != nil {
			return nil, nil, nil, h, derr
		}
		if merr := hs.ss.mixKey(shared); merr != nil {
			return nil, nil, nil, h, merr
		}
		ct, cerr := hs.ss.encryptAndHash(payload)
		if cerr != nil {
			return nil, nil, nil, h, cerr
		}
		hs.msgIndex++
		out := make([]byte, len(ePub)+len(ct))
		copy(out, ePub)
		copy(out[len(ePub):], ct)
		return out, nil, nil, h, nil

	case !hs.initiator && hs.msgIndex == 1:
		// <- e, ee
		e, gerr := hs.nextEphemeral()
		if gerr != nil {
			return nil, nil, nil, h, gerr
		}
		hs.e = e
		ePub := e.PublicKey().Bytes()
		hs.ss.mixHash(ePub)

		shared, derr := e.ECDH(hs.re)
		if derr != nil {
			return nil, nil, nil, h, derr
		}
		if merr := hs.ss.mixKey(shared); merr != nil {
			return nil, nil, nil, h, merr
		}
		ct, cerr := hs.ss.encryptAndHash(payload)
		if cerr != nil {
			return nil, nil, nil, h, cerr
		}
		hs.msgIndex++
		c1, c2, hh, serr := hs.ss.split()
		if serr != nil {
			return nil, nil, nil, h, serr
		}
		out := make([]byte, len(ePub)+len(ct))
		copy(out, ePub)
		copy(out[len(ePub):], ct)
		// Responder sends on c2, receives on c1.
		return out, c2, c1, hh, nil

	default:
		return nil, nil, nil, h, errors.New("relay: unexpected WriteMessage state")
	}
}

// ReadMessage processes the peer's next NK handshake message.
func (hs *HandshakeState) ReadMessage(msg []byte) (payload []byte, send, recv *CipherState, h [keySize]byte, err error) {
	if len(msg) < keySize {
		return nil, nil, nil, h, errShortMessage
	}
	switch {
	case !hs.initiator && hs.msgIndex == 0:
		// -> e, es
		re, perr := ecdh.X25519().NewPublicKey(msg[:keySize])
		if perr != nil {
			return nil, nil, nil, h, perr
		}
		hs.re = re
		hs.ss.mixHash(re.Bytes())

		shared, derr := hs.s.ECDH(re)
		if derr != nil {
			return nil, nil, nil, h, derr
		}
		if merr := hs.ss.mixKey(shared); merr != nil {
			return nil, nil, nil, h, merr
		}
		pt, cerr := hs.ss.decryptAndHash(msg[keySize:])
		if cerr != nil {
			return nil, nil, nil, h, cerr
		}
		hs.msgIndex++
		return pt, nil, nil, h, nil

	case hs.initiator && hs.msgIndex == 1:
		// <- e, ee
		re, perr := ecdh.X25519().NewPublicKey(msg[:keySize])
		if perr != nil {
			return nil, nil, nil, h, perr
		}
		hs.re = re
		hs.ss.mixHash(re.Bytes())

		shared, derr := hs.e.ECDH(re)
		if derr != nil {
			return nil, nil, nil, h, derr
		}
		if merr := hs.ss.mixKey(shared); merr != nil {
			return nil, nil, nil, h, merr
		}
		pt, cerr := hs.ss.decryptAndHash(msg[keySize:])
		if cerr != nil {
			return nil, nil, nil, h, cerr
		}
		hs.msgIndex++
		c1, c2, hh, serr := hs.ss.split()
		if serr != nil {
			return nil, nil, nil, h, serr
		}
		// Initiator sends on c1, receives on c2.
		return pt, c1, c2, hh, nil

	default:
		return nil, nil, nil, h, errors.New("relay: unexpected ReadMessage state")
	}
}
