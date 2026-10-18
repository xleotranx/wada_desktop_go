package main

// queueJob binds a request to the connection on show. A later switch
// cannot replay it against another server, even after an A-B-A switch.
type admission uint8

const (
	admissionAccepted admission = iota
	admissionBusy
	admissionClosed
)

func (a *app) queueJob(job agentJob) admission {
	if a.closed {
		a.commandError = "The connection is closed."
		return admissionClosed
	}
	job.route = a.route
	select {
	case a.jobs <- job:
		a.commandError = ""
		return admissionAccepted
	default:
		a.commandError = "The connection is busy. Try again."
		return admissionBusy
	}
}

// pollAgentJob checks fresh control work before replaying retry work.
// A switch replaces the queued jobs, so it is always at the channel's
// head; ordinary jobs retain their order behind the retry stash.
func pollAgentJob(jobs <-chan agentJob, stash *[]agentJob) (agentJob, bool) {
	select {
	case job := <-jobs:
		if job.kind == jobServer {
			return job, true
		}
		*stash = append(*stash, job)
	default:
	}
	if len(*stash) == 0 {
		return agentJob{}, false
	}
	job := (*stash)[0]
	(*stash)[0] = agentJob{}
	*stash = (*stash)[1:]
	return job, true
}
