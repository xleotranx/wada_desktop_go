package desktop

import "strings"

// draftKey names where the composer's unsent words live: its session, or
// the empty key for a new-chat draft.
func (a *app) draftKey() string {
	return string(a.conversation.target.session)
}

// stashDraft writes the composer into its key. Whitespace alone is not a
// draft worth keeping, and an empty composer removes only the key it was
// loaded from: a new-chat draft the user has not seen yet stays put.
func (a *app) stashDraft() {
	scope := a.scope()
	if scope.drafts == nil {
		scope.drafts = map[string]string{}
	}
	key := a.draftKey()
	if strings.TrimSpace(a.conversation.draft) == "" {
		if a.conversation.draftLoaded == key {
			delete(scope.drafts, key)
		}
		return
	}
	scope.drafts[key] = a.conversation.draft
	a.conversation.draftLoaded = key
}

// restoreDraft reads a key back into the composer.
func (a *app) restoreDraft(key string) {
	a.conversation.draft = a.scope().drafts[key]
	a.conversation.draftLoaded = key
}

// forgetDraft drops a key once its words have been sent.
func (a *app) forgetDraft(key string) {
	delete(a.scope().drafts, key)
}

// draftFor is the unsent text a row should advertise: the live composer
// for the open chat, or the stored words for another.
func (a *app) draftFor(id string) string {
	if a.draftKey() == id {
		return a.conversation.draft
	}
	return a.scope().drafts[id]
}

// hasDraft reports whether a chat or the new-chat composer holds words.
func (a *app) hasDraft(id string) bool {
	return strings.TrimSpace(a.draftFor(id)) != ""
}

// draftSnippet folds a draft onto one short line for a marker's tooltip.
func draftSnippet(text string) string {
	text = strings.Join(strings.Fields(text), " ")
	runes := []rune(text)
	if len(runes) > 64 {
		return string(runes[:64]) + "…"
	}
	return text
}
