// Package protocol defines the wire format shared by the host and the
// clients of MmouseShare.
//
// Every message is a JSON object in one line, so the traffic can be read
// with any line based tool while debugging a LAN setup.
package protocol

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
)

// Version is the version of the protocol this build speaks. A host refuses
// a client that speaks another version.
const Version = 1

// MessageType tells what a message carries.
type MessageType string

const (
	// TypeHello is the first message a client sends. It carries the name
	// of the device and the size of its screen.
	TypeHello MessageType = "hello"

	// TypeWelcome is the host's answer to a hello. It carries the
	// protocol version and the port the host listens on.
	TypeWelcome MessageType = "welcome"

	// TypeRegister asks the host to put the device on another edge of
	// the virtual desktop.
	TypeRegister MessageType = "register"

	// TypeDevices is the list of the devices the host knows about.
	TypeDevices MessageType = "devices"

	// TypeEvent carries a mouse event, from a device to the host or from
	// the host to the devices.
	TypeEvent MessageType = "event"

	// TypePointer tells where the pointer of the virtual desktop is, and
	// which screen it is on.
	TypePointer MessageType = "pointer"

	// TypeWarp asks a device to put its cursor somewhere, so that the
	// pointer can go on past the edge of its screen.
	TypeWarp MessageType = "warp"

	// TypeGrab asks the hub to hand the mouse to the device that sends
	// it, because the user moved its mouse.
	TypeGrab MessageType = "grab"

	// TypePing keeps a connection alive.
	TypePing MessageType = "ping"

	// TypePong answers a ping.
	TypePong MessageType = "pong"

	// TypeError reports a problem.
	TypeError MessageType = "error"
)

// Edge is the side of the virtual desktop a device sits on.
type Edge string

const (
	// EdgeLeft is the left of the virtual desktop.
	EdgeLeft Edge = "left"
	// EdgeRight is the right of the virtual desktop.
	EdgeRight Edge = "right"
	// EdgeTop is the top of the virtual desktop.
	EdgeTop Edge = "top"
	// EdgeBottom is the bottom of the virtual desktop.
	EdgeBottom Edge = "bottom"
)

// Valid reports whether e is an edge the protocol knows.
func (e Edge) Valid() bool {
	switch e {
	case EdgeLeft, EdgeRight, EdgeTop, EdgeBottom:
		return true
	}
	return false
}

// EventKind is the kind of a mouse event.
type EventKind string

const (
	// EventMove is a movement of the pointer.
	EventMove EventKind = "move"
	// EventButton is a press or a release of a button.
	EventButton EventKind = "button"
	// EventWheel is a turn of the wheel.
	EventWheel EventKind = "wheel"
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

// Message is one frame of the protocol.
type Message struct {
	// Type is the kind of the message.
	Type MessageType `json:"type"`

	// Version is the protocol version, in hello and welcome.
	Version int `json:"version,omitempty"`

	// Name is the name of a device, in hello.
	Name string `json:"name,omitempty"`

	// Port is the port the host listens on, in welcome.
	Port int `json:"port,omitempty"`

	// Edge is where a device sits in the virtual desktop.
	Edge Edge `json:"edge,omitempty"`

	// Width and Height are the size of the screen of a device, in
	// pixels, in hello and register.
	Width  int `json:"width,omitempty"`
	Height int `json:"height,omitempty"`

	// X and Y are a place on the screen of a device, in pixels.
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`

	// Kind is the kind of a mouse event.
	Kind EventKind `json:"kind,omitempty"`

	// DX and DY are the movement of the pointer, in pixels.
	DX int `json:"dx,omitempty"`
	DY int `json:"dy,omitempty"`

	// Button is the button of a button event.
	Button Button `json:"button,omitempty"`

	// Down tells a press from a release.
	Down bool `json:"down,omitempty"`

	// Wheel is the number of notches of a wheel event. It is positive
	// when the wheel turns up.
	Wheel int `json:"wheel,omitempty"`

	// Device is the device a message is about.
	Device string `json:"device,omitempty"`

	// Active is the screen that holds the pointer, in pointer messages.
	Active string `json:"active,omitempty"`

	// Devices is the list of the devices the host knows about.
	Devices []DeviceInfo `json:"devices,omitempty"`

	// Message is the text of an error.
	Message string `json:"message,omitempty"`
}

// DeviceInfo describes one device of the virtual desktop.
type DeviceInfo struct {
	// Name is the name of the device.
	Name string `json:"name"`
	// ID identifies the device among the ones of the host.
	ID string `json:"id"`
	// Edge is where the device sits.
	Edge Edge `json:"edge"`
	// Width and Height are the size of its screen, in pixels.
	Width  int `json:"width"`
	Height int `json:"height"`
	// X and Y are the place of the screen in the virtual desktop.
	X int `json:"x,omitempty"`
	Y int `json:"y,omitempty"`
	// Self tells that this is the device the message comes from.
	Self bool `json:"self,omitempty"`
	// Online tells that the device is connected.
	Online bool `json:"online"`
	// Active tells that the device holds the mouse at the moment.
	Active bool `json:"active,omitempty"`
}

// ErrClosed means the connection is over.
var ErrClosed = errors.New("protocol: connection closed")

// Write sends a message to w, terminated by a newline.
//
// The writer is often a bufio.Writer, so it is flushed when it is one:
// the bytes must reach the network at once, not when the buffer happens
// to be full.
func Write(w io.Writer, m Message) error {
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	data = append(data, '\n')
	if _, err := w.Write(data); err != nil {
		return err
	}
	if f, ok := w.(interface{ Flush() error }); ok {
		return f.Flush()
	}
	return nil
}

// Read reads one message from r. It returns ErrClosed when the peer is
// gone.
func Read(r *bufio.Reader) (Message, error) {
	line, err := r.ReadString('\n')
	if err != nil {
		if strings.TrimSpace(line) == "" {
			if errors.Is(err, io.EOF) {
				return Message{}, ErrClosed
			}
			return Message{}, err
		}
		// A last line without a newline is still a message.
	}
	line = strings.TrimSpace(line)
	if line == "" {
		if errors.Is(err, io.EOF) {
			return Message{}, ErrClosed
		}
		return Message{}, fmt.Errorf("protocol: empty message")
	}
	var m Message
	if err := json.Unmarshal([]byte(line), &m); err != nil {
		return Message{}, fmt.Errorf("protocol: %w", err)
	}
	return m, nil
}
