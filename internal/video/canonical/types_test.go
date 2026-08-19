package canonical

import "testing"

func TestVideoSpecInferredTaskKind(t *testing.T) {
	tests := []struct {
		name       string
		references []Reference
		want       TaskKind
	}{
		{name: "text", want: TaskKindTextToVideo},
		{name: "first frame", references: []Reference{{Role: "first_frame"}}, want: TaskKindFirstFrame},
		{name: "first and last frame", references: []Reference{{Role: "first_frame"}, {Role: "last_frame"}}, want: TaskKindFirstLastFrame},
		{name: "multimodal", references: []Reference{{Role: "reference_image"}}, want: TaskKindMultimodal},
		{name: "video extension", references: []Reference{{Role: "source_video"}}, want: TaskKindVideoExtension},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := &VideoSpec{References: test.references}
			if got := spec.InferredTaskKind(); got != test.want {
				t.Fatalf("task kind = %q, want %q", got, test.want)
			}
		})
	}
}
