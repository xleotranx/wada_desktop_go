package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"wada-desktop-go/internal/protocol"
	"wada-desktop-go/internal/relay"
)

func TestIsUnauthorized(t *testing.T) {
	refused := &protocol.RPCError{Code: protocol.ErrCodeUnauthorized, Message: "invalid daemon credential or password"}
	if !IsUnauthorized(refused) || !IsUnauthorized(errors.Join(errors.New("dial direct: refused"), refused)) {
		t.Fatal("a daemon refusal is not unauthorized")
	}
	if IsUnauthorized(&protocol.RPCError{Code: protocol.ErrCodeInternal}) || IsUnauthorized(errors.New("connection refused")) {
		t.Fatal("other failures read as unauthorized")
	}

	// A relay that refuses the relay token.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	_, err := DialRelay(context.Background(), srv.URL, [32]byte{1}, Options{RelayAuthToken: "wrong"})
	if !errors.Is(err, relay.ErrUnauthorized) || !IsUnauthorized(err) {
		t.Fatalf("relay refusal = %v", err)
	}
}
