// Package execution contains provider-independent execution state machines.
// SQL repositories must apply the same transitions with an optimistic version
// predicate and append the corresponding event in one transaction.
package execution

import "fmt"

type CallState string

const (
	CallReceived      CallState = "received"
	CallInProgress    CallState = "in_progress"
	CallRetryPending  CallState = "retry_pending"
	CallCompleted     CallState = "completed"
	CallFailed        CallState = "failed"
	CallCancelled     CallState = "cancelled"
	CallIndeterminate CallState = "indeterminate"
)

type AttemptState string

const (
	AttemptStarted           AttemptState = "started"
	AttemptRecoveryPending   AttemptState = "recovery_pending"
	AttemptCompleted         AttemptState = "completed"
	AttemptFailed            AttemptState = "failed"
	AttemptCancelled         AttemptState = "cancelled"
	AttemptNotCreated        AttemptState = "not_created"
	AttemptTerminatedUnknown AttemptState = "terminated_unknown"
)

type AsyncState string

const (
	AsyncAllocated         AsyncState = "allocated"
	AsyncSubmitting        AsyncState = "submitting"
	AsyncSubmissionUnknown AsyncState = "submission_unknown"
	AsyncAccepted          AsyncState = "accepted"
	AsyncRunning           AsyncState = "running"
	AsyncManualReview      AsyncState = "manual_review"
	AsyncCancelRequested   AsyncState = "cancel_requested"
	AsyncCancelUnknown     AsyncState = "cancel_unknown"
	AsyncSucceeded         AsyncState = "succeeded"
	AsyncFailed            AsyncState = "failed"
	AsyncCancelled         AsyncState = "cancelled"
	AsyncNotCreated        AsyncState = "not_created"
	AsyncTerminatedUnknown AsyncState = "terminated_unknown"
)

var callTransitions = map[CallState]map[CallState]struct{}{
	CallReceived:      {CallInProgress: {}, CallRetryPending: {}, CallCancelled: {}},
	CallInProgress:    {CallRetryPending: {}, CallCompleted: {}, CallFailed: {}, CallCancelled: {}, CallIndeterminate: {}},
	CallRetryPending:  {CallInProgress: {}, CallCancelled: {}},
	CallIndeterminate: {CallCompleted: {}, CallFailed: {}, CallCancelled: {}},
}

var attemptTransitions = map[AttemptState]map[AttemptState]struct{}{
	AttemptStarted:         {AttemptRecoveryPending: {}, AttemptCompleted: {}, AttemptFailed: {}, AttemptCancelled: {}, AttemptNotCreated: {}, AttemptTerminatedUnknown: {}},
	AttemptRecoveryPending: {AttemptCompleted: {}, AttemptFailed: {}, AttemptCancelled: {}, AttemptTerminatedUnknown: {}},
}

var asyncTransitions = map[AsyncState]map[AsyncState]struct{}{
	AsyncAllocated:         {AsyncSubmitting: {}, AsyncNotCreated: {}},
	AsyncSubmitting:        {AsyncSubmissionUnknown: {}, AsyncAccepted: {}, AsyncFailed: {}, AsyncNotCreated: {}},
	AsyncSubmissionUnknown: {AsyncAccepted: {}, AsyncManualReview: {}, AsyncNotCreated: {}, AsyncTerminatedUnknown: {}},
	AsyncAccepted:          {AsyncRunning: {}, AsyncSucceeded: {}, AsyncFailed: {}, AsyncCancelRequested: {}, AsyncCancelled: {}},
	AsyncRunning:           {AsyncSucceeded: {}, AsyncFailed: {}, AsyncCancelRequested: {}, AsyncCancelled: {}},
	AsyncManualReview:      {AsyncAccepted: {}, AsyncSucceeded: {}, AsyncFailed: {}, AsyncTerminatedUnknown: {}},
	AsyncCancelRequested:   {AsyncCancelled: {}, AsyncCancelUnknown: {}, AsyncSucceeded: {}, AsyncFailed: {}},
	AsyncCancelUnknown:     {AsyncCancelled: {}, AsyncSucceeded: {}, AsyncFailed: {}, AsyncTerminatedUnknown: {}},
}

func TransitionCall(from, to CallState) error { return transition("call", callTransitions, from, to) }
func TransitionAttempt(from, to AttemptState) error {
	return transition("attempt", attemptTransitions, from, to)
}
func TransitionAsync(from, to AsyncState) error {
	return transition("async execution", asyncTransitions, from, to)
}

func transition[T comparable](kind string, graph map[T]map[T]struct{}, from, to T) error {
	if _, ok := graph[from][to]; ok {
		return nil
	}
	return fmt.Errorf("%s transition %v -> %v is not allowed", kind, from, to)
}

// AttemptTracker enforces the single-active-attempt invariant in memory. The
// database version of this rule uses a conditional unique active marker.
type AttemptTracker struct {
	CallState       CallState
	StateVersion    uint64
	ActiveAttemptID uint64
}

func (t *AttemptTracker) BeginAttempt(attemptID uint64) error {
	if attemptID == 0 || t.ActiveAttemptID != 0 {
		return fmt.Errorf("cannot begin attempt %d: an active attempt already exists or ID is empty", attemptID)
	}
	if t.CallState != CallReceived && t.CallState != CallRetryPending && t.CallState != CallInProgress {
		return fmt.Errorf("cannot begin attempt from call state %q", t.CallState)
	}
	if t.CallState != CallInProgress {
		t.CallState = CallInProgress
	}
	t.ActiveAttemptID = attemptID
	t.StateVersion++
	return nil
}

func (t *AttemptTracker) FinishAttempt(attemptID uint64, attemptState AttemptState, nextCallState CallState) error {
	if attemptID == 0 || t.ActiveAttemptID != attemptID {
		return fmt.Errorf("attempt %d is not the active attempt", attemptID)
	}
	if attemptState != AttemptCompleted && attemptState != AttemptFailed && attemptState != AttemptCancelled && attemptState != AttemptNotCreated && attemptState != AttemptTerminatedUnknown {
		return fmt.Errorf("attempt %q is not terminal", attemptState)
	}
	if err := TransitionCall(t.CallState, nextCallState); err != nil {
		return err
	}
	t.CallState = nextCallState
	t.ActiveAttemptID = 0
	t.StateVersion++
	return nil
}
