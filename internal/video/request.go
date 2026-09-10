package video

import "errors"

var (
	ErrInvalidTaskRequest   = errors.New("invalid video task request")
	ErrEstimateNotSupported = errors.New("video estimate is not supported")
	ErrCancelNotSupported   = errors.New("video task cancellation is not supported")
	ErrActionNotSupported   = errors.New("video task action is not supported")
)

// CreateTaskRequest is the normalized public video request shared by the
// canonical codec and unified runtime boundary.
type CreateTaskRequest struct {
	UserID      uint
	TokenID     uint
	CallID      string
	RequestID   string
	Endpoint    string
	Operation   string
	Model       string
	Prompt      string
	Resolution  string
	Ratio       string
	Duration    int
	Audio       bool
	TaskMode    string
	ServiceTier string
	Content     []ContentItem
	Params      map[string]any
	Callback    string
}
