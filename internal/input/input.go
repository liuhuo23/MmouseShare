// Package input captures the mouse of the computer and moves the mouse of
// another one.
//
// The capture and the injection are the two halves of the sharing: one
// reads the movements of a real mouse, the other writes them into the
// mouse of the system.
package input

import (
	"errors"
	"sync"
)

// Kind is the kind of a mouse event.
type Kind uint8

const (
	// KindMove is the movement of the pointer.
	KindMove Kind = iota
	// KindButton is the press or the release of a button.
	KindButton
	// KindWheel is a turn of the wheel.
	KindWheel
)

// Button is a mouse button.
type Button uint8

const (
	// ButtonLeft is the left button.
	ButtonLeft Button = 1 << iota
	// ButtonRight is the right button.
	ButtonRight
	// ButtonMiddle is the middle button.
	ButtonMiddle
)

// Event is one mouse event.
type Event struct {
	// Kind is the kind of the event.
	Kind Kind
	// DX and DY are the movement of the pointer, in pixels.
	DX int
	DY int
	// X and Y are the position of the pointer on the screen, in pixels.
	X int
	Y int
	// Button is the button of a button event.
	Button Button
	// Down tells a press from a release.
	Down bool
	// Wheel is the number of notches of a wheel event. It is positive
	// when the wheel turns up.
	Wheel int
}

// ErrUnsupported means the platform has no capture or injection.
var ErrUnsupported = errors.New("input: not supported on this platform")

// ErrAccessibility means that the system waits for the person to allow the
// program to read and to post the input of the computer. macOS keeps that
// permission in the accessibility part of its settings.
var ErrAccessibility = errors.New("input: MmouseShare needs the permission of the accessibility on macOS, it is in the settings of the system")

// Screen is a screen of the computer.
type Screen struct {
	// X and Y are the position of the screen on the desktop.
	X int
	Y int
	// Width and Height are the size of the screen, in pixels.
	Width  int
	Height int
}

// Backend captures the mouse of the computer and moves the mouse of it.
//
// A backend is used by the side that owns the real mouse to read it, and by
// the side that follows to write the movements of another mouse into it.
type Backend interface {
	// Capture starts reading the mouse of the computer and calls onEvent
	// for every event, until Stop is called.
	Capture(onEvent func(Event)) error

	// Stop stops reading the mouse.
	Stop() error

	// Move moves the pointer of the computer by a number of pixels.
	Move(dx, dy int) error

	// MoveTo puts the pointer of the computer at a place of the screen.
	MoveTo(x, y int) error

	// Button presses or releases a button of the computer.
	Button(b Button, down bool) error

	// Wheel turns the wheel of the computer.
	Wheel(notches int) error

	// Screens returns the screens of the computer.
	Screens() []Screen

	// Primary returns the screen with the pointer on it.
	Primary() Screen

	// ShowCursor shows the cursor of the computer or hides it.
	//
	// Only the screen that holds the shared pointer shows a cursor, so
	// the cursor of the other screens is hidden while the pointer is
	// somewhere else.
	ShowCursor(show bool) error
}

// Factory builds the backend of a platform. It is set by the file of the
// platform, so that the rest of the program never names one.
var Factory func() (Backend, error)

// New returns the backend of the platform the program runs on.
func New() (Backend, error) {
	if Factory == nil {
		return nil, ErrUnsupported
	}
	return Factory()
}

// Recorder collects the events of a backend, for the tests and for the
// interface, which shows the events of the device.
type Recorder struct {
	mu     sync.Mutex
	events []Event
}

// Record adds an event to the list.
func (r *Recorder) Record(e Event) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = append(r.events, e)
}

// Events returns the events recorded so far.
func (r *Recorder) Events() []Event {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Event, len(r.events))
	copy(out, r.events)
	return out
}

// Reset forgets the events recorded so far.
func (r *Recorder) Reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.events = nil
}
