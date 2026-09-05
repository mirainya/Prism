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
