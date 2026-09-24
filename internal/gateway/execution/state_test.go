package execution

import "testing"

func TestExecutionStateGraphs(t *testing.T) {
	valid := []struct {
		name string
		fn   func() error
	}{
		{"call retry", func() error { return TransitionCall(CallInProgress, CallRetryPending) }},
		{"attempt recovery", func() error { return TransitionAttempt(AttemptStarted, AttemptRecoveryPending) }},
		{"unknown submission", func() error { return TransitionAsync(AsyncSubmitting, AsyncSubmissionUnknown) }},
		{"cancel race success", func() error { return TransitionAsync(AsyncCancelRequested, AsyncSucceeded) }},
	}
	for _, test := range valid {
		t.Run(test.name, func(t *testing.T) {
			if err := test.fn(); err != nil {
				t.Fatal(err)
			}
		})
	}
	if err := TransitionCall(CallCompleted, CallInProgress); err == nil {
		t.Fatal("completed call was reactivated")
	}
	if err := TransitionAsync(AsyncSubmitting, AsyncRunning); err == nil {
		t.Fatal("submission skipped accepted state")
	}
}

func TestTerminatedUnknownCanResolveFromLateEvidence(t *testing.T) {
	for _, state := range []AsyncState{AsyncAccepted, AsyncRunning} {
		t.Run("unreachable_from_"+string(state), func(t *testing.T) {
			if err := TransitionAsync(state, AsyncTerminatedUnknown); err != nil {
				t.Fatal(err)
			}
		})
	}

	attemptTargets := []AttemptState{AttemptCompleted, AttemptFailed, AttemptCancelled, AttemptNotCreated}
	for _, target := range attemptTargets {
		t.Run("attempt_to_"+string(target), func(t *testing.T) {
			if err := TransitionAttempt(AttemptTerminatedUnknown, target); err != nil {
				t.Fatal(err)
			}
		})
	}

	asyncTargets := []AsyncState{AsyncSucceeded, AsyncFailed, AsyncCancelled, AsyncNotCreated}
	for _, target := range asyncTargets {
		t.Run("async_to_"+string(target), func(t *testing.T) {
			if err := TransitionAsync(AsyncTerminatedUnknown, target); err != nil {
				t.Fatal(err)
			}
		})
	}

	if err := TransitionAttempt(AttemptTerminatedUnknown, AttemptRecoveryPending); err == nil {
		t.Fatal("terminated attempt was reactivated")
	}
	if err := TransitionAsync(AsyncTerminatedUnknown, AsyncRunning); err == nil {
		t.Fatal("terminated async execution was reactivated")
	}
}

func TestAttemptTrackerAllowsOnlyOneActiveAttempt(t *testing.T) {
	tracker := AttemptTracker{CallState: CallReceived, StateVersion: 1}
	if err := tracker.BeginAttempt(10); err != nil {
		t.Fatal(err)
	}
	if err := tracker.BeginAttempt(11); err == nil {
		t.Fatal("second active attempt was accepted")
	}
	if err := tracker.FinishAttempt(10, AttemptCompleted, CallCompleted); err != nil {
		t.Fatal(err)
	}
	if tracker.ActiveAttemptID != 0 || tracker.CallState != CallCompleted {
		t.Fatalf("unexpected tracker state: %#v", tracker)
	}
}
