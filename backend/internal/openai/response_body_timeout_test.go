package openai

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIdleTimeoutBodyUnblocksStalledTransport(t *testing.T) {
	stopped := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		close(stopped)
	}))
	defer server.Close()
	resp, err := server.Client().Get(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	body := &idleTimeoutBody{body: resp.Body, timeout: 20 * time.Millisecond}
	defer body.Close()
	_, err = io.ReadAll(body)
	if !errors.Is(err, errUpstreamBodyIdleTimeout) {
		t.Fatalf("read error = %v, want idle timeout", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("idle timeout did not release the upstream connection")
	}
}

func TestIdleTimeoutBodyAllowsProgressBeyondTimeout(t *testing.T) {
	reader, writer := io.Pipe()
	body := &idleTimeoutBody{body: reader, timeout: 100 * time.Millisecond}
	defer body.Close()
	defer writer.Close()
	go func() {
		defer writer.Close()
		for i := 0; i < 8; i++ {
			time.Sleep(25 * time.Millisecond)
			if _, err := writer.Write([]byte("x")); err != nil {
				return
			}
		}
	}()
	started := time.Now()
	data, err := io.ReadAll(body)
	if err != nil || string(data) != "xxxxxxxx" {
		t.Fatalf("read = %q, %v", data, err)
	}
	if time.Since(started) <= body.timeout {
		t.Fatal("test did not exercise a response longer than the idle timeout")
	}
}
