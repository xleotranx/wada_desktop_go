// Package protocol defines Wada's append-only WebSocket JSON wire envelopes,
// capability flags, RPC schemas, and binary stream frames shared between the
// headless daemon (wadad), Go clients, CLI, and Web/PWA clients.
package protocol

import "encoding/json"

const ProtocolVersion = 1

// Client capabilities advertised in hello and server_info.
const (
	CapOwnedSubscriptions = "owned_subscriptions"
	CapCreationReceipts   = "creation_receipts"
)

// Top-level WebSocket text envelope types.
const (
	TypeHello       = "hello"
	TypeServerInfo  = "server_info"
	TypePing        = "ping"
	TypePong        = "pong"
	TypeRPCRequest  = "rpc.request"
	TypeRPCResponse = "rpc.response"
	TypeEvent       = "event"
)

// Standard RPC error codes.
const (
	ErrCodeUnauthorized   = "unauthorized"
	ErrCodeNotFound       = "not_found"
	ErrCodeConflict       = "conflict"
	ErrCodeNotSent        = "not_sent"
	ErrCodeOutcomeUnknown = "outcome_unknown"
	ErrCodeInvalid        = "invalid"
	ErrCodeInternal       = "internal"
)

// Envelope is the top-level JSON WebSocket text frame.
type Envelope struct {
	Type           string          `json:"type"`
	RequestID      string          `json:"requestId,omitempty"`
	Method         string          `json:"method,omitempty"`
	SubscriptionID string          `json:"subscriptionId,omitempty"`
	Payload        json.RawMessage `json:"payload,omitempty"`
	Error          *RPCError       `json:"error,omitempty"`
}

// RPCError is a structured failure returned on rpc.response or rejection.
type RPCError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	if e.Code == "" {
		return e.Message
	}
	return e.Code + ": " + e.Message
}

// HelloPayload is the initial client handshake frame.
type HelloPayload struct {
	ClientID        string   `json:"clientId"`
	ClientType      string   `json:"clientType"` // "desktop" | "web" | "cli"
	ProtocolVersion int      `json:"protocolVersion"`
	AppVersion      string   `json:"appVersion,omitempty"`
	Credential      string   `json:"credential,omitempty"`
	Password        string   `json:"password,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

// ServerInfoPayload is the server's admission response to hello.
type ServerInfoPayload struct {
	ServerID        string   `json:"serverId"`
	Hostname        string   `json:"hostname"`
	Version         string   `json:"version"`
	ProtocolVersion int      `json:"protocolVersion"`
	Origin          string   `json:"origin"`
	Features        []string `json:"features,omitempty"`
}

// PingPongPayload carries optional millisecond timestamps for liveness probes.
type PingPongPayload struct {
	Timestamp int64 `json:"timestamp,omitempty"`
}
