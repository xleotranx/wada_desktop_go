package relay

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type noiseVector struct {
	ProtocolName string `json:"protocolName"`
	PrologueHex  string `json:"prologueHex"`
	RsPrivHex    string `json:"rsPrivHex"`
	RsPubHex     string `json:"rsPubHex"`
	EiPrivHex    string `json:"eiPrivHex"`
	EiPubHex     string `json:"eiPubHex"`
	ErPrivHex    string `json:"erPrivHex"`
	ErPubHex     string `json:"erPubHex"`
	Msg1PlainHex string `json:"msg1PlainHex"`
	Msg1WireHex  string `json:"msg1WireHex"`
	Msg2PlainHex string `json:"msg2PlainHex"`
	Msg2WireHex  string `json:"msg2WireHex"`
	HandshakeH   string `json:"handshakeHashHex"`
	InitSendKey  string `json:"initSendKeyHex"`
	InitRecvKey  string `json:"initRecvKeyHex"`
	AppPlainHex  string `json:"appPlainHex"`
	AppCipherHex string `json:"appCipherHex"`
}

func fixedKey(t *testing.T, fill byte) *ecdh.PrivateKey {
	t.Helper()
	var b [32]byte
	for i := range b {
		b[i] = fill + byte(i)
	}
	k, err := ecdh.X25519().NewPrivateKey(b[:])
	if err != nil {
		t.Fatal(err)
	}
	return k
}

func TestNoiseNKHandshakeAndTransportSecurity(t *testing.T) {
	kp, err := GenerateKeypair()
	if err != nil {
		t.Fatal(err)
	}
	initHS, err := NewInitiator(kp.Public, []byte("prologue-1"))
	if err != nil {
		t.Fatal(err)
	}
	respHS, err := NewResponder(kp.Private, []byte("prologue-1"))
	if err != nil {
		t.Fatal(err)
	}

	msg1, _, _, _, err := initHS.WriteMessage([]byte("hi-resp"))
	if err != nil {
		t.Fatal(err)
	}
	p1, _, _, _, err := respHS.ReadMessage(msg1)
	if err != nil {
		t.Fatal(err)
	}
	if string(p1) != "hi-resp" {
		t.Fatalf("p1 = %q", p1)
	}

	msg2, rSend, rRecv, rH, err := respHS.WriteMessage([]byte("hi-init"))
	if err != nil {
		t.Fatal(err)
	}
	p2, iSend, iRecv, iH, err := initHS.ReadMessage(msg2)
	if err != nil {
		t.Fatal(err)
	}
	if string(p2) != "hi-init" {
		t.Fatalf("p2 = %q", p2)
	}
	if rH != iH {
		t.Fatal("handshake hashes do not match")
	}

	// Bidirectional encrypted frames with monotonic counters.
	for i, msg := range []string{"frame-0", "frame-1", "frame-2"} {
		ct, err := iSend.EncryptWithAD(nil, []byte(msg))
		if err != nil {
			t.Fatal(err)
		}
		// Tamper must fail without advancing receiver nonce.
		tampered := append([]byte(nil), ct...)
		tampered[len(tampered)-1] ^= 0xff
		if _, err := rRecv.DecryptWithAD(nil, tampered); err == nil {
			t.Fatalf("tampered frame %d accepted", i)
		}

		pt, err := rRecv.DecryptWithAD(nil, ct)
		if err != nil {
			t.Fatalf("valid frame %d failed after tamper: %v", i, err)
		}
		if string(pt) != msg {
			t.Fatalf("pt = %q, want %q", pt, msg)
		}

		// Replaying an earlier frame must fail.
		if _, err := rRecv.DecryptWithAD(nil, ct); err == nil {
			t.Fatalf("replayed frame %d accepted", i)
		}
	}

	respCT, err := rSend.EncryptWithAD(nil, []byte("reply-0"))
	if err != nil {
		t.Fatal(err)
	}
	respPT, err := iRecv.DecryptWithAD(nil, respCT)
	if err != nil || string(respPT) != "reply-0" {
		t.Fatalf("respPT = %q, err=%v", respPT, err)
	}
}

func TestNoiseNKRejectsWrongKeyOrPrologue(t *testing.T) {
	kp1, _ := GenerateKeypair()
	kp2, _ := GenerateKeypair()

	initHS, _ := NewInitiator(kp1.Public, nil)
	wrongResp, _ := NewResponder(kp2.Private, nil)
	msg1, _, _, _, err := initHS.WriteMessage([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, _, _, err := wrongResp.ReadMessage(msg1); err == nil {
		t.Fatal("expected responder with wrong static key to reject msg1")
	}

	// Mismatched prologue must fail msg1 authentication.
	initP, _ := NewInitiator(kp1.Public, []byte("a"))
	respP, _ := NewResponder(kp1.Private, []byte("b"))
	m, _, _, _, _ := initP.WriteMessage(nil)
	if _, _, _, _, err := respP.ReadMessage(m); err == nil {
		t.Fatal("expected mismatched prologue to reject msg1")
	}
}

func TestNoiseNKGoldenVectorFixture(t *testing.T) {
	rs := fixedKey(t, 0x10)
	ei := fixedKey(t, 0x40)
	er := fixedKey(t, 0x70)

	var rsPriv, rsPub [32]byte
	copy(rsPriv[:], rs.Bytes())
	copy(rsPub[:], rs.PublicKey().Bytes())
	prologue := []byte("wada-relay-v1")

	initHS, err := NewInitiator(rsPub, prologue)
	if err != nil {
		t.Fatal(err)
	}
	initHS.genEphemeral = func() (*ecdh.PrivateKey, error) { return ei, nil }

	respHS, err := NewResponder(rsPriv, prologue)
	if err != nil {
		t.Fatal(err)
	}
	respHS.genEphemeral = func() (*ecdh.PrivateKey, error) { return er, nil }

	msg1Plain := []byte("hello-daemon")
	msg2Plain := []byte("hello-client")
	appPlain := []byte("rpc:status")

	msg1, _, _, _, err := initHS.WriteMessage(msg1Plain)
	if err != nil {
		t.Fatal(err)
	}
	if got, _, _, _, err := respHS.ReadMessage(msg1); err != nil || !bytes.Equal(got, msg1Plain) {
		t.Fatalf("ReadMessage(msg1) = %x, err=%v", got, err)
	}
	msg2, rSend, _, rH, err := respHS.WriteMessage(msg2Plain)
	if err != nil {
		t.Fatal(err)
	}
	got2, iSend, iRecv, iH, err := initHS.ReadMessage(msg2)
	if err != nil || !bytes.Equal(got2, msg2Plain) || rH != iH {
		t.Fatalf("ReadMessage(msg2) mismatch: err=%v", err)
	}
	appCT, err := iSend.EncryptWithAD(nil, appPlain)
	if err != nil {
		t.Fatal(err)
	}
	_ = rSend

	vec := noiseVector{
		ProtocolName: ProtocolName,
		PrologueHex:  hex.EncodeToString(prologue),
		RsPrivHex:    hex.EncodeToString(rsPriv[:]),
		RsPubHex:     hex.EncodeToString(rsPub[:]),
		EiPrivHex:    hex.EncodeToString(ei.Bytes()),
		EiPubHex:     hex.EncodeToString(ei.PublicKey().Bytes()),
		ErPrivHex:    hex.EncodeToString(er.Bytes()),
		ErPubHex:     hex.EncodeToString(er.PublicKey().Bytes()),
		Msg1PlainHex: hex.EncodeToString(msg1Plain),
		Msg1WireHex:  hex.EncodeToString(msg1),
		Msg2PlainHex: hex.EncodeToString(msg2Plain),
		Msg2WireHex:  hex.EncodeToString(msg2),
		HandshakeH:   hex.EncodeToString(iH[:]),
		InitSendKey:  hex.EncodeToString(iSend.key[:]),
		InitRecvKey:  hex.EncodeToString(iRecv.key[:]),
		AppPlainHex:  hex.EncodeToString(appPlain),
		AppCipherHex: hex.EncodeToString(appCT),
	}

	fixturePath := filepath.Join("testdata", "noise_nk_vector.json")
	if os.Getenv("WADA_UPDATE_VECTORS") == "1" {
		if err := os.MkdirAll(filepath.Dir(fixturePath), 0o755); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.MarshalIndent(vec, "", "  ")
		if err := os.WriteFile(fixturePath, append(raw, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	raw, err := os.ReadFile(fixturePath)
	if err != nil {
		t.Fatalf("read golden fixture %s: %v", fixturePath, err)
	}
	var want noiseVector
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	if vec != want {
		t.Fatalf("Noise NK golden vector drift:\n got: %+v\nwant: %+v", vec, want)
	}
}
