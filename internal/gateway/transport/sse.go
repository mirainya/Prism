package transport

import (
	"bufio"
	"context"
	"io"
	"strings"
)

// SSEFrame is one fully assembled server-sent event. Multiple data fields are
// joined with newlines as required by the SSE specification.
type SSEFrame struct {
	Event string
	Data  []byte
}

// SSEReader reads complete SSE frames without imposing provider semantics.
type SSEReader struct {
	reader *bufio.Reader
	event  string
	data   []string
	eof    bool
}

func NewSSEReader(reader io.Reader) *SSEReader {
	return &SSEReader{reader: bufio.NewReader(reader)}
}

func (r *SSEReader) Next(ctx context.Context) (SSEFrame, error) {
	if r == nil || r.reader == nil {
		return SSEFrame{}, io.EOF
	}
	for {
		if err := ctx.Err(); err != nil {
			return SSEFrame{}, err
		}
		if r.eof {
			return SSEFrame{}, io.EOF
		}

		line, err := r.reader.ReadString('\n')
		if err != nil && err != io.EOF {
			return SSEFrame{}, err
		}
		if err == io.EOF {
			r.eof = true
		}
		line = strings.TrimSuffix(line, "\n")
		line = strings.TrimSuffix(line, "\r")

		if line == "" {
			if len(r.data) > 0 {
				return r.dispatch(), nil
			}
			r.event = ""
			if r.eof {
				return SSEFrame{}, io.EOF
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			if r.eof && len(r.data) > 0 {
				return r.dispatch(), nil
			}
			if r.eof {
				return SSEFrame{}, io.EOF
			}
			continue
		}

		field, value, found := strings.Cut(line, ":")
		if !found {
			value = ""
		}
		value = strings.TrimPrefix(value, " ")
		switch field {
		case "event":
			r.event = value
		case "data":
			r.data = append(r.data, value)
		}
		if r.eof {
			if len(r.data) > 0 {
				return r.dispatch(), nil
			}
			return SSEFrame{}, io.EOF
		}
	}
}

func (r *SSEReader) dispatch() SSEFrame {
	frame := SSEFrame{Event: r.event, Data: []byte(strings.Join(r.data, "\n"))}
	r.event = ""
	r.data = nil
	return frame
}
