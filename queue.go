package main

// queueTurn remembers a prompt the runner accepted while another turn
// was active. The runner processes one job at a time, so the prompt waits
// its turn; this count explains the pause in the composer.
func (a *app) queueTurn(id string) {
	if id == "" {
		return
	}
	scope := a.scope()
	if scope.queued == nil {
		scope.queued = map[string]int{}
	}
	scope.queued[id]++
}

// queuedTurns counts the prompts waiting behind a chat's active turn.
func (a *app) queuedTurns(id string) int {
	return a.scope().queued[id]
}

// startQueuedTurn releases one waiting prompt when its turn starts, from
// whichever chat the stream reports.
func (a *app) startQueuedTurn(id string) {
	scope := a.scope()
	if scope.queued[id] <= 0 {
		return
	}
	if scope.queued[id] == 1 {
		delete(scope.queued, id)
		return
	}
	scope.queued[id]--
}
