package main

import "strings"

// draftKey names where the composer's unsent words live: its session, or
// the empty key for a new-chat draft.
func (a *app) draftKey() string {
	return string(a.conversation.target.session)
}

// stashDraft writes the composer into its key. Whitespace alone is not a
// draft worth keeping.
func (a *app) stashDraft() {
	scope := a.scope()
	if scope.drafts == nil {
		scope.drafts = map[string]string{}
	}
	key := a.draftKey()
	if strings.TrimSpace(a.conversation.draft) == "" {
		delete(scope.drafts, key)
		return
	}
	scope.drafts[key] = a.conversation.draft
}

// restoreDraft reads a key back into the composer.
func (a *app) restoreDraft(key string) {
	a.conversation.draft = a.scope().drafts[key]
}

// forgetDraft drops a key once its words have been sent.
func (a *app) forgetDraft(key string) {
	delete(a.scope().drafts, key)
}
