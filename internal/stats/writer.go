package stats

import (
	"io"
	"sync/atomic"
)

// CountingWriter wraps an io.Writer and atomically counts bytes written.
type CountingWriter struct {
	inner   io.Writer
	counter *atomic.Int64
}

// NewCountingWriter wraps w, accumulating byte counts into counter.
func NewCountingWriter(w io.Writer, counter *atomic.Int64) *CountingWriter {
	return &CountingWriter{inner: w, counter: counter}
}

func (cw *CountingWriter) Write(p []byte) (int, error) {
	n, err := cw.inner.Write(p)
	if n > 0 {
		cw.counter.Add(int64(n))
	}
	return n, err
}
