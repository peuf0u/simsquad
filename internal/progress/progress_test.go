package progress

import (
	"io"
	"testing"
	"time"
)

func TestLoggerCloseWithoutEventsReturns(t *testing.T) {
	t.Parallel()

	done := make(chan struct{})
	go func() {
		logger := NewWith(io.Discard)
		logger.Close()
		logger.Close()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("Close without events hung")
	}
}
