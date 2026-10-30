// Package attachments declares access to images waiting in the composer.
package attachments

import (
	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
	"github.com/xleotranx/wada-kit/kernel"
)

// Image limits bound decoding and PNG replacement before pixels are allocated.
const (
	MaxImageBytes  = 10 << 20
	MaxImageSide   = 16384
	MaxImagePixels = 16_000_000
)

var (
	ServiceKey = kernel.NewKey[Service]("attachments.images")
	Actions    = kernel.NewPoint[Action]("attachments.image-actions")
	Changed    = kernel.NewTopic[struct{}]("attachments.changed")
)

// Image is an immutable snapshot of a pending image. IDs are unique within
// the provider's activation; Revision changes whenever the image is replaced.
// Notes, when set, tell the agent what the image shows; they go out with the
// message beneath a line naming the image, and leave with it when it is
// removed.
type Image struct {
	ID, Revision uint64
	File         agent.File
	Notes        string
}

// Service is called on the UI thread. Processing and encoding image data
// happens on a worker before Replace; source files are never overwritten.
type Service interface {
	Snapshot(id uint64) (Image, bool)
	// Attach adds an image made by a plugin, such as a screenshot, to the
	// next message and returns its identity. It refuses what a dropped file
	// would be refused for. Preview was prepared off the UI thread and must
	// show file. It emits Changed.
	Attach(file agent.File, preview *ui.Bitmap) (uint64, error)
	// Replace puts file, an image, notes and details in place of source and
	// its details. It refuses a stale identity/revision, a type the agent
	// does not take, or more files or bytes than a message takes. Preview
	// was prepared off the UI thread and must show file. Successful
	// replacement emits Changed.
	Replace(source Image, file agent.File, preview *ui.Bitmap, notes string, details []Detail) error
}

// Detail is part of an image sent at full resolution right after it, so
// what the agent's model sees scaled down stays legible. Name says what it
// shows, such as "mark 1 at full resolution"; it goes out with the message
// as "[Image 3: mark 1 at full resolution, from image 2]".
type Detail struct {
	Name string
	File agent.File
}

// Action appears beside each pending image. Run executes through its owning
// plugin's scope and receives the pending image's identity.
type Action struct {
	Title string
	Run   func(id uint64)
}
