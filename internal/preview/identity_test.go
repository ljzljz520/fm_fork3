package preview

import (
	"context"
	"testing"
)

func TestCounterMonotonic(t *testing.T) {
	var c Counter

	if c.Request() != 0 || c.Size() != 0 {
		t.Fatal("zero counter should start at generation 0")
	}

	first := c.NextRequest()

	if first != 1 || c.Request() != 1 {
		t.Fatalf("expected request 1, got %d", first)
	}

	if c.NextRequest() != 2 {
		t.Fatal("request ids must be monotonic")
	}

	if c.NextSize() != 1 || c.NextSize() != 2 || c.Size() != 2 {
		t.Fatal("size versions must be monotonic and independent of requests")
	}

	// Advancing sizes must not affect the request generation.
	if c.Request() != 2 {
		t.Fatalf("size advancement altered request id: %d", c.Request())
	}
}

func TestAborted(t *testing.T) {
	if Aborted(nil) {
		t.Fatal("nil context must be treated as alive")
	}

	ctx, cancel := context.WithCancel(context.Background())

	if Aborted(ctx) {
		t.Fatal("fresh context must be alive")
	}

	cancel()

	if !Aborted(ctx) {
		t.Fatal("cancelled context must be reported as aborted")
	}
}
