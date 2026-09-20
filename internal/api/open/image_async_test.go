package open

import "testing"

func TestImageTaskPublicStatus(t *testing.T) {
	tests := []struct {
		name       string
		callStatus string
		taskStatus string
		want       string
	}{
		{name: "received call", callStatus: "received", want: "processing"},
		{name: "queued projection", callStatus: "in_progress", taskStatus: "queued", want: "queued"},
		{name: "allocated projection", callStatus: "in_progress", taskStatus: "allocated", want: "processing"},
		{name: "submitting projection", callStatus: "in_progress", taskStatus: "submitting", want: "processing"},
		{name: "running projection", callStatus: "in_progress", taskStatus: "running", want: "processing"},
		{name: "submission unknown remains active", callStatus: "in_progress", taskStatus: "submission_unknown", want: "processing"},
		{name: "manual review remains active", callStatus: "in_progress", taskStatus: "manual_review", want: "processing"},
		{name: "succeeded projection", callStatus: "in_progress", taskStatus: "succeeded", want: "completed"},
		{name: "failed projection", callStatus: "in_progress", taskStatus: "not_created", want: "failed"},
		{name: "completed call wins", callStatus: "completed", taskStatus: "running", want: "completed"},
		{name: "failed call wins", callStatus: "failed", taskStatus: "succeeded", want: "failed"},
		{name: "cancelled call is terminal", callStatus: "cancelled", taskStatus: "running", want: "failed"},
		{name: "indeterminate call is terminal", callStatus: "indeterminate", taskStatus: "manual_review", want: "failed"},
		{name: "unknown state remains queryable", callStatus: "", taskStatus: "", want: "processing"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := imageTaskPublicStatus(test.callStatus, test.taskStatus); got != test.want {
				t.Fatalf("status %s/%s = %s, want %s", test.callStatus, test.taskStatus, got, test.want)
			}
		})
	}
}
