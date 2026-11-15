package api

import (
	"context"
	"errors"
	"sync"
)

// errSuperseded is the cancel cause when a newer commit's review replaces a run on the same PR.
var errSuperseded = errors.New("superseded by a newer push")

type prRun struct {
	sha    string
	cancel context.CancelCauseFunc
}

// prRuns tracks the in-flight review per PR on this machine, so a newer push stops paying for an older one.
// Other machines are covered by the head check before posting, not by this map.
type prRuns struct {
	mu   sync.Mutex
	runs map[string]*prRun
}

func newPRRuns() *prRuns { return &prRuns{runs: map[string]*prRun{}} }

var activeRuns = newPRRuns()

// start cancels an older-commit run of the same PR and registers this one; done deregisters it and frees its context.
func (r *prRuns) start(parent context.Context, key, sha string) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	me := &prRun{sha: sha, cancel: cancel}

	r.mu.Lock()
	if old, ok := r.runs[key]; ok && old.sha != sha {
		old.cancel(errSuperseded)
	}
	r.runs[key] = me
	r.mu.Unlock()

	return ctx, func() {
		r.mu.Lock()
		if r.runs[key] == me {
			delete(r.runs, key)
		}
		r.mu.Unlock()
		cancel(nil)
	}
}
