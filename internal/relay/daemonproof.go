package relay

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"errors"
)

// A daemon proves to wada-relay that it holds the static private key behind the
// server hash it registers, so nobody who merely knows the hash (it is in every
// pairing link) can take over the daemon's slot.
//
// The relay sends RelayOpChallenge with an ephemeral X25519 public key and a
// random nonce. The daemon answers RelayOpProof with its static public key and
// an HMAC keyed by a hash of X25519(static private, relay ephemeral). The relay
// checks that the public key matches the hash in the URL and recomputes the MAC
// from its ephemeral private key. Neither side learns anything a pairing link
// does not already carry.

const (
	daemonProofLabel     = "wada-relay-daemon-proof-v1"
	daemonChallengeSize  = keySize + 32 // relay ephemeral public key || nonce
	daemonProofSize      = keySize + sha256.Size
	daemonProofKeyPrefix = daemonProofLabel + " key"
)

var errBadDaemonProof = errors.New("relay: daemon key proof failed")

// newDaemonChallenge returns the relay's ephemeral key and the challenge payload.
func newDaemonChallenge() (*ecdh.PrivateKey, []byte, error) {
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	challenge := make([]byte, daemonChallengeSize)
	copy(challenge, eph.PublicKey().Bytes())
	if _, err := rand.Read(challenge[keySize:]); err != nil {
		return nil, nil, err
	}
	return eph, challenge, nil
}

// answerDaemonChallenge proves possession of kp's private key for challenge.
func answerDaemonChallenge(kp Keypair, challenge []byte) ([]byte, error) {
	if len(challenge) != daemonChallengeSize {
		return nil, errBadDaemonProof
	}
	priv, err := ecdh.X25519().NewPrivateKey(kp.Private[:])
	if err != nil {
		return nil, err
	}
	relayEph, err := ecdh.X25519().NewPublicKey(challenge[:keySize])
	if err != nil {
		return nil, err
	}
	shared, err := priv.ECDH(relayEph)
	if err != nil {
		return nil, err
	}
	proof := make([]byte, 0, daemonProofSize)
	proof = append(proof, kp.Public[:]...)
	return append(proof, daemonProofMAC(shared, challenge, kp.Public[:])...), nil
}

// verifyDaemonProof checks that proof answers challenge with the private key
// behind the static public key whose ServerPubHash is serverHash.
func verifyDaemonProof(eph *ecdh.PrivateKey, challenge []byte, serverHash string, proof []byte) error {
	if len(proof) != daemonProofSize {
		return errBadDaemonProof
	}
	var pub [keySize]byte
	copy(pub[:], proof[:keySize])
	if ServerPubHash(pub) != serverHash {
		return errBadDaemonProof
	}
	daemonPub, err := ecdh.X25519().NewPublicKey(pub[:])
	if err != nil {
		return errBadDaemonProof
	}
	shared, err := eph.ECDH(daemonPub)
	if err != nil {
		return errBadDaemonProof
	}
	if !hmac.Equal(proof[keySize:], daemonProofMAC(shared, challenge, pub[:])) {
		return errBadDaemonProof
	}
	return nil
}

func daemonProofMAC(shared, challenge, pub []byte) []byte {
	// Hash the shared secret under a label first, so the MAC key is never the
	// raw DH output that Noise handshakes with the same static key also use.
	keyHash := sha256.New()
	keyHash.Write([]byte(daemonProofKeyPrefix))
	keyHash.Write(shared)
	mac := hmac.New(sha256.New, keyHash.Sum(nil))
	mac.Write([]byte(daemonProofLabel))
	mac.Write(challenge)
	mac.Write(pub)
	return mac.Sum(nil)
}
