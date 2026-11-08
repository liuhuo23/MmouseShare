//go:build windows

package input

import "testing"

// currentCursor returns the handle of the cursor of the computer, which is
// nothing when the cursor is hidden.
func currentCursor() uintptr {
	handle, _, _ := procGetCursor.Call()
	return handle
}

// TestShowCursorHidesTheCursor makes sure that the cursor of the computer
// can be hidden and shown again, which is what keeps the pointer from
// looking like it moves on two screens at once.
func TestShowCursorHidesTheCursor(t *testing.T) {
	b, err := New()
	if err != nil {
		t.Fatal(err)
	}

	// The cursor is given back when the test ends, whatever happens.
	defer func() { _ = b.ShowCursor(true) }()

	if err := b.ShowCursor(true); err != nil {
		t.Fatal(err)
	}
	if currentCursor() == 0 {
		t.Skip("the cursor of the computer cannot be read")
	}

	if err := b.ShowCursor(false); err != nil {
		t.Fatal(err)
	}
	if h := currentCursor(); h != 0 {
		t.Errorf("the cursor of the computer is still there, want it to be hidden")
	}

	if err := b.ShowCursor(true); err != nil {
		t.Fatal(err)
	}
	if h := currentCursor(); h == 0 {
		t.Errorf("the cursor of the computer is still hidden, want it to be shown")
	}
}
