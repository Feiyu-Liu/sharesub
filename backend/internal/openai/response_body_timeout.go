package openai

import (
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

const (
	upstreamBodyIdleTimeout = 180 * time.Second
	imageBodyIdleTimeout    = 900 * time.Second
)

var errUpstreamBodyIdleTimeout = errors.New("upstream response body idle timeout")

// idleTimeoutBody bounds blocked upstream reads without limiting total response
// duration or counting time spent writing downstream. Closing the transport body
// unblocks Read, including reads made while draining a rejected response.
type idleTimeoutBody struct {
	body      io.ReadCloser
	timeout   time.Duration
	closeOnce sync.Once
	closeErr  error
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	var state atomic.Uint32 // 0: reading, 1: completed, 2: timed out
	timer := time.AfterFunc(b.timeout, func() {
		if state.CompareAndSwap(0, 2) {
			_ = b.Close()
		}
	})
	n, err := b.body.Read(p)
	completed := state.CompareAndSwap(0, 1)
	timer.Stop()
	if !completed {
		return n, errUpstreamBodyIdleTimeout
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.closeOnce.Do(func() { b.closeErr = b.body.Close() })
	return b.closeErr
}
