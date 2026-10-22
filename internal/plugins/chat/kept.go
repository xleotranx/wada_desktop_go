package chat

import (
	"encoding/json"
	"hash/maphash"
	"slices"

	"wada-desktop-go/internal/contract/agent"

	"github.com/egoist/mygo/ui"
)

// keptLimit is how many conversations the user left keep their transcript
// in memory, besides the open one.
const keptLimit = 8

// kept is a conversation the user left, as it was on show: coming back to it
// shows it at once, scrolled where it was, while it is read again.
type kept struct {
	id       agent.SessionID
	t        transcript
	list     ui.ListState
	expanded map[uint64]bool
	worked   map[uint64]bool
}

// keptSet holds the conversations left most recently, newest first.
type keptSet []*kept

func (s keptSet) find(id agent.SessionID) *kept {
	if i := slices.IndexFunc(s, func(k *kept) bool { return k.id == id }); i >= 0 {
		return s[i]
	}
	return nil
}

// take removes id's entry and returns it, or nil.
func (s *keptSet) take(id agent.SessionID) *kept {
	i := slices.IndexFunc(*s, func(k *kept) bool { return k.id == id })
	if i < 0 {
		return nil
	}
	k := (*s)[i]
	*s = slices.Delete(*s, i, i+1)
	return k
}

// put keeps k first, dropping the oldest past keptLimit.
func (s *keptSet) put(k *kept) {
	s.take(k.id)
	*s = slices.Insert(*s, 0, k)
	if len(*s) > keptLimit {
		clear((*s)[keptLimit:])
		*s = (*s)[:keptLimit]
	}
}

var readSeed = maphash.MakeSeed()

// readSum identifies what a read returned, so that reading a conversation
// again rebuilds the transcript only when it changed. It is zero when it
// cannot tell, which matches nothing.
func readSum(messages []agent.Message) uint64 {
	raw, err := json.Marshal(messages)
	if err != nil {
		return 0
	}
	return maphash.Bytes(readSeed, raw) | 1
}
