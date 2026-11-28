package protocol

const (
	MethodPreviewPending = "preview.pending"
	MethodPreviewClaim   = "preview.claim"
	MethodPreviewReply   = "preview.reply"
)

type PreviewRequest struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Session   string `json:"session"`
	Service   string `json:"service"`
	Origin    string `json:"origin"`
	Action    string `json:"action"`
	Value     string `json:"value,omitempty"`
}

type PreviewReply struct {
	ID    string `json:"id"`
	Text  string `json:"text,omitempty"`
	Error string `json:"error,omitempty"`
}
