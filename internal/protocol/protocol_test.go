package protocol

import (
	"bufio"
	"bytes"
	"strings"
	"testing"
)

// TestWriteAndRead makes sure that a message survives the trip.
func TestWriteAndRead(t *testing.T) {
	var buf bytes.Buffer
	want := Message{
		Type:   TypeEvent,
		Kind:   EventButton,
		Button: ButtonLeft,
		Down:   true,
		X:      12,
		Y:      34,
	}
	if err := Write(&buf, want); err != nil {
		t.Fatal(err)
	}
	got, err := Read(bufio.NewReader(&buf))
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != want.Type || got.Kind != want.Kind || got.Button != want.Button {
		t.Errorf("the message came back as %+v, want %+v", got, want)
	}
	if !got.Down {
		t.Error("the press came back as a release")
	}
	if got.X != 12 || got.Y != 34 {
		t.Errorf("the place came back as %d,%d, want 12,34", got.X, got.Y)
	}
}

// TestReadOfALineWithoutANewline makes sure that the last message of a
// connection is read even when the peer leaves without closing the line.
func TestReadOfALineWithoutANewline(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(`{"type":"ping"}`))
	got, err := Read(r)
	if err != nil {
		t.Fatal(err)
	}
	if got.Type != TypePing {
		t.Errorf("the message came back as %s, want ping", got.Type)
	}
}

// TestReadOfAClosedConnection makes sure that the end of a connection is
// told apart from a broken one.
func TestReadOfAClosedConnection(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(""))
	if _, err := Read(r); err != ErrClosed {
		t.Errorf("the error is %v, want ErrClosed", err)
	}
}

// TestEdgeValid makes sure that only the edges of the protocol are taken.
func TestEdgeValid(t *testing.T) {
	for _, e := range []Edge{EdgeLeft, EdgeRight, EdgeTop, EdgeBottom} {
		if !e.Valid() {
			t.Errorf("the edge %s is not taken", e)
		}
	}
	if Edge("sideways").Valid() {
		t.Error("an edge that does not exist is taken")
	}
}
