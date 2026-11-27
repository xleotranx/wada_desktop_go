package protocol

import (
	"bytes"
	"encoding/json"
	"testing"
)

func TestEnvelopeAndEventRoundTrip(t *testing.T) {
	ev := Event{
		Epoch:     2,
		Seq:       14,
		RequestID: "req-1",
		Kind:      EventToolCalled,
		Session:   "s1",
		Tool:      &Tool{ID: "t1", Name: "bash", Kind: ToolShell, Command: "go test ./..."},
	}
	payload, err := json.Marshal(ev)
	if err != nil {
		t.Fatal(err)
	}
	env := Envelope{
		Type:           TypeEvent,
		SubscriptionID: "sub-1",
		Payload:        payload,
	}
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	var gotEnv Envelope
	if err := json.Unmarshal(raw, &gotEnv); err != nil {
		t.Fatal(err)
	}
	if gotEnv.Type != TypeEvent || gotEnv.SubscriptionID != "sub-1" {
		t.Fatalf("unexpected envelope: %+v", gotEnv)
	}
	var gotEv Event
	if err := json.Unmarshal(gotEnv.Payload, &gotEv); err != nil {
		t.Fatal(err)
	}
	if gotEv.Epoch != 2 || gotEv.Seq != 14 || gotEv.Tool == nil || gotEv.Tool.Kind != ToolShell {
		t.Fatalf("unexpected event: %+v", gotEv)
	}
}

func TestBinaryFrameCodec(t *testing.T) {
	in := []byte("hello terminal\n")
	frame := EncodeBinaryFrame(OpTermOutput, 3, in)
	op, ch, payload, err := DecodeBinaryFrame(frame)
	if err != nil {
		t.Fatal(err)
	}
	if op != OpTermOutput || ch != 3 || !bytes.Equal(payload, in) {
		t.Fatalf("decoded (%x, %d, %q)", op, ch, payload)
	}
	if _, _, _, err := DecodeBinaryFrame([]byte{0x01}); err == nil {
		t.Fatal("expected error for truncated frame")
	}
}
