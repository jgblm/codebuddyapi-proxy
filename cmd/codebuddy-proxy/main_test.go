package main

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"
)

type testUpdateHandoff struct {
	committed  bool
	restarted  bool
	message    string
	commitErr  error
	restartErr error
}

func (h *testUpdateHandoff) Commit() error {
	h.committed = true
	return h.commitErr
}

func (h *testUpdateHandoff) RestartOriginal(message string) error {
	h.restarted, h.message = true, message
	return h.restartErr
}

func TestFinishUpdateRestartsOriginalAfterDrainTimeout(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	entered, release := make(chan struct{}), make(chan struct{})
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(entered)
		<-release
		_, _ = io.WriteString(w, "done")
	})}
	served := make(chan error, 1)
	go func() { served <- srv.Serve(listener) }()
	client := &http.Client{Timeout: 5 * time.Second}
	requestDone := make(chan error, 1)
	go func() {
		resp, err := client.Get("http://" + listener.Addr().String())
		if err == nil {
			_, err = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
		requestDone <- err
	}()
	t.Cleanup(func() {
		close(release)
		srv.Close()
		client.CloseIdleConnections()
		select {
		case <-requestDone:
		case <-time.After(5 * time.Second):
			t.Error("request did not exit")
		}
	})
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	handoff := &testUpdateHandoff{}
	var drainErr error
	err = finishUpdate(ctx, func(ctx context.Context) error {
		drainErr = srv.Shutdown(ctx)
		return drainErr
	}, handoff)
	if !errors.Is(drainErr, context.DeadlineExceeded) {
		t.Fatalf("expected drain timeout, got %v", drainErr)
	}
	if err != nil || handoff.committed || !handoff.restarted {
		t.Fatalf("recovery not handed off: err=%v handoff=%+v", err, handoff)
	}
	if !strings.Contains(handoff.message, "更新排空失败") || !strings.Contains(handoff.message, drainErr.Error()) {
		t.Fatalf("missing failure reason: %q", handoff.message)
	}
	select {
	case err := <-served:
		if !errors.Is(err, http.ErrServerClosed) {
			t.Fatalf("unexpected serve error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listener was not closed")
	}
}

func TestFinishUpdateCommitsOnlyAfterSuccessfulDrain(t *testing.T) {
	handoff := &testUpdateHandoff{}
	if err := finishUpdate(t.Context(), func(context.Context) error { return nil }, handoff); err != nil {
		t.Fatal(err)
	}
	if !handoff.committed || handoff.restarted {
		t.Fatalf("unexpected handoff: %+v", handoff)
	}
}

func TestFinishUpdateReportsRecoveryHandoffFailure(t *testing.T) {
	drainErr, restartErr := errors.New("drain failed"), errors.New("helper unavailable")
	handoff := &testUpdateHandoff{restartErr: restartErr}
	err := finishUpdate(t.Context(), func(context.Context) error { return drainErr }, handoff)
	if !errors.Is(err, drainErr) || !strings.Contains(err.Error(), restartErr.Error()) || handoff.committed {
		t.Fatalf("unexpected failure: %v handoff=%+v", err, handoff)
	}
}
