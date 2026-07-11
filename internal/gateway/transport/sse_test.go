package transport

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestSSEReaderAssemblesFramesAndFinalData(t *testing.T) {
	reader := NewSSEReader(strings.NewReader(": keepalive\r\nevent: update\r\ndata: first\r\ndata: second\r\n\r\ndata: final"))
	first, err := reader.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if first.Event != "update" || string(first.Data) != "first\nsecond" {
		t.Fatalf("first frame = %#v", first)
	}
	last, err := reader.Next(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if last.Event != "" || string(last.Data) != "final" {
		t.Fatalf("last frame = %#v", last)
	}
	if _, err = reader.Next(context.Background()); !errors.Is(err, io.EOF) {
		t.Fatalf("expected EOF, got %v", err)
	}
}

func TestSSEReaderHonorsCancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewSSEReader(strings.NewReader("data: ignored\n\n")).Next(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
