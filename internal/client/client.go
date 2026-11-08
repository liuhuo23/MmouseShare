// Package client connects a device to a hub and shares the mouse with it.
package client

import (
	"bufio"
	"fmt"
	"net"
	"sync"
	"time"

	"mmouseshare/internal/protocol"
)

// The defaults of the client.
const (
	// DefaultPort is the port the hub listens on.
	DefaultPort = 27301
	// dialTimeout is how long the client waits for the hub.
	dialTimeout = 5 * time.Second
)

// Client is a device connected to a hub.
type Client struct {
	mu sync.Mutex

	// name is the name of this device.
	name string
	// width and height are the size of the screen of this device.
	width  int
	height int
	// edge is where the device sits in the virtual desktop.
	edge protocol.Edge

	conn net.Conn
	rw   *bufio.ReadWriter

	// onEvent is called for every mouse event the hub sends.
	onEvent func(protocol.Message)
	// onPointer is called when the hub says where the pointer is.
	onPointer func(device, active string, x, y int)
	// onWarp is called when the hub says the cursor has to move, so that
	// the pointer can go on past the edge of the screen.
	onWarp func(x, y int)
	// onDevices is called when the hub says which devices it knows.
	onDevices func([]protocol.DeviceInfo)
	// onError is called when the connection fails.
	onError func(error)

	closed bool
	done   chan struct{}
}

// New returns a client that is not connected yet.
func New(name string) *Client {
	return &Client{name: name}
}

// OnEvent sets the function called for every mouse event.
func (c *Client) OnEvent(fn func(protocol.Message)) { c.onEvent = fn }

// OnPointer sets the function called when the pointer moves.
func (c *Client) OnPointer(fn func(device, active string, x, y int)) { c.onPointer = fn }

// OnWarp sets the function called when the cursor has to move.
func (c *Client) OnWarp(fn func(x, y int)) { c.onWarp = fn }

// OnDevices sets the function called when the list of devices changes.
func (c *Client) OnDevices(fn func([]protocol.DeviceInfo)) { c.onDevices = fn }

// OnError sets the function called when the connection fails.
func (c *Client) OnError(fn func(error)) { c.onError = fn }

// Connect joins the hub at the given address.
func (c *Client) Connect(addr string) error {
	conn, err := net.DialTimeout("tcp", addr, dialTimeout)
	if err != nil {
		return fmt.Errorf("client: connect: %w", err)
	}
	c.conn = conn
	c.rw = bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	c.done = make(chan struct{})

	if err := protocol.Write(c.rw, protocol.Message{
		Type:    protocol.TypeHello,
		Version: protocol.Version,
		Name:    c.name,
		Edge:    c.edge,
		Width:   c.width,
		Height:  c.height,
	}); err != nil {
		_ = conn.Close()
		return fmt.Errorf("client: hello: %w", err)
	}

	go c.read()
	return nil
}

// SetScreen tells the client the size of the screen of this device.
func (c *Client) SetScreen(width, height int) {
	c.mu.Lock()
	c.width = width
	c.height = height
	c.mu.Unlock()
}

// SetEdge tells the client where the device sits in the virtual desktop.
func (c *Client) SetEdge(edge protocol.Edge) {
	c.mu.Lock()
	c.edge = edge
	c.mu.Unlock()
}

// Send moves the pointer, or presses a button, for the hub.
func (c *Client) Send(m protocol.Message) error {
	c.mu.Lock()
	closed := c.closed
	rw := c.rw
	c.mu.Unlock()
	if closed || rw == nil {
		return fmt.Errorf("client: not connected")
	}
	return protocol.Write(rw, m)
}

// Move sends a movement of the mouse, and where the cursor of this device
// is, so that the host can tell where the pointer is on the virtual
// desktop.
func (c *Client) Move(dx, dy, x, y int) error {
	return c.Send(protocol.Message{
		Type: protocol.TypeEvent,
		Kind: protocol.EventMove,
		DX:   dx,
		DY:   dy,
		X:    x,
		Y:    y,
	})
}

// Button sends the press or the release of a button.
func (c *Client) Button(btn protocol.Button, down bool) error {
	return c.Send(protocol.Message{
		Type:   protocol.TypeEvent,
		Kind:   protocol.EventButton,
		Button: btn,
		Down:   down,
	})
}

// Wheel sends a turn of the wheel.
func (c *Client) Wheel(notches int) error {
	return c.Send(protocol.Message{
		Type:  protocol.TypeEvent,
		Kind:  protocol.EventWheel,
		Wheel: notches,
	})
}

// read takes the messages of the hub.
func (c *Client) read() {
	defer func() {
		c.mu.Lock()
		c.closed = true
		onError := c.onError
		c.mu.Unlock()
		if onError != nil {
			onError(fmt.Errorf("client: the connection is over"))
		}
	}()

	for {
		msg, err := protocol.Read(c.rw.Reader)
		if err != nil {
			return
		}
		switch msg.Type {
		case protocol.TypeEvent:
			c.mu.Lock()
			fn := c.onEvent
			c.mu.Unlock()
			if fn != nil {
				fn(msg)
			}
		case protocol.TypePointer:
			c.mu.Lock()
			fn := c.onPointer
			c.mu.Unlock()
			if fn != nil {
				fn(msg.Device, msg.Active, msg.X, msg.Y)
			}
		case protocol.TypeWarp:
			c.mu.Lock()
			fn := c.onWarp
			c.mu.Unlock()
			if fn != nil {
				fn(msg.X, msg.Y)
			}
		case protocol.TypeDevices:
			c.mu.Lock()
			fn := c.onDevices
			c.mu.Unlock()
			if fn != nil {
				fn(msg.Devices)
			}
		case protocol.TypeWelcome:
			// The hub accepted the client.
		case protocol.TypeError:
			c.mu.Lock()
			onError := c.onError
			c.mu.Unlock()
			if onError != nil {
				onError(fmt.Errorf("client: %s", msg.Message))
			}
			return
		}
	}
}

// Close leaves the hub.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil
	}
	c.closed = true
	conn := c.conn
	c.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}
