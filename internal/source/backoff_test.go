package source

import (
	"testing"
	"time"
)

func TestBackoffIsBoundedAndPerSource(t *testing.T) {
	backoff := NewBackoff(time.Second, 4*time.Second)
	for index, want := range []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, 4 * time.Second} {
		if got := backoff.Failure("warehouse"); got != want {
			t.Fatalf("attempt %d = %s, want %s", index, got, want)
		}
	}
	if got := backoff.Failure("analytics"); got != time.Second {
		t.Fatalf("independent source delay = %s", got)
	}
	backoff.Ready("warehouse")
	if got := backoff.Failure("warehouse"); got != time.Second {
		t.Fatalf("reset source delay = %s", got)
	}
}
