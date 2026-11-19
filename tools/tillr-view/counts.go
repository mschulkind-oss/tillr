package main

// boardCountsOf is the header's four figures, computed from the store's rows alone.
//
// The snapshot and `tillr view status --store` both call it, so the two read faces cannot
// disagree about a number the board leads with (design §5.6). A caller that recomputed any
// of the four itself would be a second arithmetic, which is the defect the shared function
// exists to prevent.
//
// runs is the runs the caller will show, including the live run a leased lane projects
// (liveRuns): a run the store has no row for is still a run that is live. waiting is the
// blocked_by edges, by item. holds is the loop's latest question-hold reason per item (see
// questionHolds), so an item the planner holds for a ruling is counted as blocked here
// rather than as an in-queue row the header would offer as dispatchable. history is the
// planner's read of each item's attempt history, so Dispatchable is the planner's own
// predicate rather than a second arithmetic (see dispatchHistory.dispatchable).
func boardCountsOf(items []storeItem, runs []storeRun, candidates []storeCandidate, attempts []storeAttempt, waiting map[string][]string, holds map[string]string, history *dispatchHistory) boardCounts {
	counts := boardCounts{ByState: map[string]int{}}
	runsByItem := map[string][]storeRun{}
	for _, run := range runs {
		runsByItem[run.ItemID] = append(runsByItem[run.ItemID], run)
		if run.State == liveRunState {
			counts.RunsLive++
		}
	}
	enqueued := enqueuedCandidates(candidates, attempts)
	stateOf := itemStates(items)
	for _, it := range items {
		var enqueuedCandidate *storeCandidate
		if c, ok := enqueued[it.ItemID]; ok {
			enqueuedCandidate = &c
		}
		hold := holds[it.ItemID]
		projected := ProjectItemState(it, enqueuedCandidate, activeRun(runsByItem[it.ItemID]), waiting[it.ItemID], hold)
		// The projection keeps a terminal item terminal, so a stale hold on a landed row is
		// not a question the header still owes. Count the holds the projection applied.
		if projected.Hold != "" {
			counts.QuestionHolds++
		}
		// Dispatchable is the drain planner's own rule, not the projection's: an in-queue
		// item whose every blocker has reached a terminal state is one the planner would
		// dispatch, while the projection renders any blocked_by edge as blocked. The two
		// are allowed to differ, and the header should say what the queue will actually do.
		// A question hold and a retry hold are the planner's own reasons not to dispatch, so
		// both are excluded -- the second through the planner's mayBeARetry rule.
		if history != nil && history.dispatchable(it, stateOf, waiting, hold) {
			counts.Dispatchable++
		}
		counts.ByState[projected.State]++
		if projected.State == "blocked" {
			counts.Blocked++
		}
	}
	return counts
}

// enqueuedCandidates answers the first enqueued candidate per item whose attempt has not
// reached a terminal row: the candidate the projection reads as the item's queue entry.
func enqueuedCandidates(candidates []storeCandidate, attempts []storeAttempt) map[string]storeCandidate {
	terminal := map[string]bool{}
	for _, a := range attempts {
		terminal[a.CandidateID] = true
	}
	enqueued := map[string]storeCandidate{}
	for _, c := range candidates {
		if c.State == "enqueued" && !terminal[c.CandidateID] {
			if _, seen := enqueued[c.ItemID]; !seen {
				enqueued[c.ItemID] = c
			}
		}
	}
	return enqueued
}

// activeRun is the run among an item's runs that is still running, or nil. The last one
// wins, because the store's runs are ordered by start and the newest is the one working.
func activeRun(runs []storeRun) *storeRun {
	var active *storeRun
	for i := range runs {
		if runs[i].State == liveRunState {
			active = &runs[i]
		}
	}
	return active
}

// itemStates indexes the store's raw state by item id, which is what a blocker edge
// resolves against.
func itemStates(items []storeItem) map[string]string {
	stateOf := make(map[string]string, len(items))
	for _, it := range items {
		stateOf[it.ItemID] = it.State
	}
	return stateOf
}

// unmetBlocker answers whether an item waits on a blocker that has not landed or been
// resolved elsewhere. Closing an item without landing does not satisfy a blocked_by edge,
// and an edge naming an item this store does not carry is unmet: a queue that dispatched
// an item whose dependency never landed would be guessing.
func unmetBlocker(stateOf map[string]string, waiting map[string][]string, itemID string) bool {
	for _, blockedBy := range waiting[itemID] {
		state := stateOf[blockedBy]
		if state == "closed" || !terminalItemStates[state] {
			return true
		}
	}
	return false
}
