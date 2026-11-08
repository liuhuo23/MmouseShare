package session

import (
	"testing"
	"time"

	"mmouseshare/internal/protocol"
)

// TestSetEdgeReturns guards the lock of the session: SetEdge reads the size
// of the screen through the half of the pair that does not take the lock
// again, because the window of the program calls it and stops with it.
func TestSetEdgeReturns(t *testing.T) {
	s := New("test", "id", ModeClient)
	done := make(chan error, 1)
	go func() { done <- s.SetEdge(protocol.EdgeLeft) }()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("setting the edge: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("setting the edge did not return: the lock of the session is taken twice")
	}

	if got := s.Edge(); got != protocol.EdgeLeft {
		t.Errorf("the edge is %q, want %q", got, protocol.EdgeLeft)
	}
}

// TestSetEdgeNotifies tells that the window hears about the new edge.
func TestSetEdgeNotifies(t *testing.T) {
	s := New("test", "id", ModeHost)
	changed := make(chan struct{}, 1)
	s.OnChange(func() { changed <- struct{}{} })

	if err := s.SetEdge(protocol.EdgeTop); err != nil {
		t.Fatalf("setting the edge: %v", err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("the listener did not hear about the new edge")
	}
}
