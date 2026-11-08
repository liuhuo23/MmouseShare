// Package session puts the pieces of the sharing together: the mouse of
// this device, the hub of the network and the devices on the other side.
package session

import (
	"fmt"
	"log"
	"sync"
	"time"

	"mmouseshare/internal/client"
	"mmouseshare/internal/discovery"
	"mmouseshare/internal/hub"
	"mmouseshare/internal/input"
	"mmouseshare/internal/protocol"
	"mmouseshare/internal/virtual"
)

// Mode tells whether the device holds the mouse of the network or follows
// another one.
type Mode int

const (
	// ModeHost means the device runs the hub of the network.
	ModeHost Mode = iota
	// ModeClient means the device follows the mouse of a hub.
	ModeClient
)

// Session is the sharing of one device.
type Session struct {
	mu sync.Mutex

	// mode is what the device does.
	mode Mode
	// name is the name of this device.
	name string
	// id identifies this device.
	id string
	// edge is where this device sits in the virtual desktop.
	edge protocol.Edge

	// backend reads and moves the mouse of this device.
	backend input.Backend
	// hub is the hub of the network, in host mode.
	hub *hub.Hub
	// client is the connection to the hub, in client mode.
	client *client.Client
	// scanner looks for the hubs of the network.
	scanner *discovery.Scanner
	// announcer says that this device is a hub.
	announcer *discovery.Announcer

	// capturing tells that the mouse of this device is read.
	capturing bool
	// devices are the devices of the virtual desktop.
	devices []protocol.DeviceInfo
	// controller is the device whose mouse holds the pointer.
	controller string
	// active is the screen the pointer is on.
	active string
	// pointer is where the pointer is, in the virtual desktop.
	pointer virtual.Point
	// cursorX and cursorY are the place of the cursor of this device, on
	// its own screen.
	cursorX int
	cursorY int
	// cursorKnown tells that the place of the cursor is known, which the
	// first movement of the mouse says.
	cursorKnown bool

	// onChange is called when the state of the session changes.
	onChange func()
}

// New returns a session for this device.
func New(name, id string, mode Mode) *Session {
	return &Session{
		name: name,
		id:   id,
		mode: mode,
		edge: protocol.EdgeRight,
	}
}

// OnChange sets the function called when the state changes.
func (s *Session) OnChange(fn func()) {
	s.mu.Lock()
	s.onChange = fn
	s.mu.Unlock()
}

// notify tells the listener that the state changed.
func (s *Session) notify() {
	s.mu.Lock()
	fn := s.onChange
	s.mu.Unlock()
	if fn != nil {
		fn()
	}
}

// Mode returns what the device does.
func (s *Session) Mode() Mode {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.mode
}

// SetMode changes what the device does.
func (s *Session) SetMode(mode Mode) error {
	s.mu.Lock()
	if s.mode == mode {
		s.mu.Unlock()
		return nil
	}
	s.mode = mode
	s.mu.Unlock()

	s.Stop()
	return nil
}

// Edge returns where this device sits in the virtual desktop.
func (s *Session) Edge() protocol.Edge {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.edge
}

// SetEdge changes where this device sits in the virtual desktop.
func (s *Session) SetEdge(edge protocol.Edge) error {
	s.mu.Lock()
	s.edge = edge
	c := s.client
	// The width and the height of the screen come from the locked half of
	// the pair: the lock of the session is held here already, and taking
	// it again would stop this goroutine, and the window that called it,
	// for good.
	width := s.screenWidthLocked()
	height := s.screenHeightLocked()
	s.mu.Unlock()

	if c != nil {
		if err := c.Send(protocol.Message{
			Type:   protocol.TypeRegister,
			Edge:   edge,
			Width:  width,
			Height: height,
		}); err != nil {
			return err
		}
	}
	s.notify()
	return nil
}

// Devices returns the devices of the virtual desktop.
func (s *Session) Devices() []protocol.DeviceInfo {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]protocol.DeviceInfo, len(s.devices))
	copy(out, s.devices)
	return out
}

// Controller returns the device whose mouse holds the pointer.
func (s *Session) Controller() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controller
}

// Active returns the screen the pointer is on.
func (s *Session) Active() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.active
}

// Pointer returns where the pointer is, in the virtual desktop.
func (s *Session) Pointer() virtual.Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.pointer
}

// StartHost runs the hub of the network and starts reading the mouse.
func (s *Session) StartHost(port int) error {
	backend, err := input.New()
	if err != nil {
		return err
	}
	h, err := hub.New(s.name, s.id, port)
	if err != nil {
		return err
	}
	screens := backend.Screens()
	if len(screens) > 0 {
		h.SetHostScreen(screens[0].Width, screens[0].Height)
	}

	s.mu.Lock()
	s.backend = backend
	s.hub = h
	s.mode = ModeHost
	s.mu.Unlock()

	// When the pointer walks off the edge of the screen of this device,
	// the cursor of the mouse goes back to the other edge, so that the
	// mouse can go on.
	h.SetLocalWarp(func(x, y int) {
		s.mu.Lock()
		s.cursorX, s.cursorY = x, y
		s.mu.Unlock()
		if backend != nil {
			_ = backend.MoveTo(x, y)
		}
	})

	if err := h.Listen(); err != nil {
		return err
	}

	announcer, err := discovery.NewAnnouncer(s.name, h.Port(), screens[0].Width, screens[0].Height)
	if err != nil {
		log.Println("session: the devices will not find this one:", err)
	} else {
		s.mu.Lock()
		s.announcer = announcer
		s.mu.Unlock()
	}

	if err := s.startCapture(); err != nil {
		return err
	}
	s.notify()
	return nil
}

// StartClient joins the hub at the given address.
func (s *Session) StartClient(addr string) error {
	backend, err := input.New()
	if err != nil {
		return err
	}
	c := client.New(s.name)
	screens := backend.Screens()
	if len(screens) > 0 {
		c.SetScreen(screens[0].Width, screens[0].Height)
	}
	c.SetEdge(s.edge)

	c.OnEvent(s.onRemoteEvent)
	c.OnPointer(s.onRemotePointer)
	c.OnDevices(s.onRemoteDevices)
	c.OnWarp(s.onRemoteWarp)
	c.OnError(func(err error) {
		log.Println("session:", err)
		s.notify()
	})

	s.mu.Lock()
	s.backend = backend
	s.client = c
	s.mode = ModeClient
	s.mu.Unlock()

	if err := c.Connect(addr); err != nil {
		return err
	}
	s.notify()
	return nil
}

// Scan looks for the hubs of the local network.
func (s *Session) Scan() error {
	scanner, err := discovery.NewScanner()
	if err != nil {
		return err
	}
	scanner.OnFound(func(discovery.Device) { s.notify() })
	s.mu.Lock()
	s.scanner = scanner
	s.mu.Unlock()
	return nil
}

// Found returns the hubs that were found on the network.
func (s *Session) Found() []discovery.Device {
	s.mu.Lock()
	scanner := s.scanner
	s.mu.Unlock()
	if scanner == nil {
		return nil
	}
	return scanner.Devices()
}

// Stop stops everything the session does.
func (s *Session) Stop() {
	s.mu.Lock()
	backend := s.backend
	h := s.hub
	c := s.client
	scanner := s.scanner
	announcer := s.announcer
	s.backend = nil
	s.hub = nil
	s.client = nil
	s.scanner = nil
	s.announcer = nil
	s.capturing = false
	s.mu.Unlock()

	if backend != nil {
		_ = backend.Stop()
	}
	if h != nil {
		_ = h.Close()
	}
	if c != nil {
		_ = c.Close()
	}
	if scanner != nil {
		_ = scanner.Close()
	}
	if announcer != nil {
		_ = announcer.Close()
	}
	s.notify()
}

// startCapture starts reading the mouse of this device.
func (s *Session) startCapture() error {
	s.mu.Lock()
	backend := s.backend
	if backend == nil {
		s.mu.Unlock()
		return fmt.Errorf("session: no mouse to read")
	}
	if s.capturing {
		s.mu.Unlock()
		return nil
	}
	s.capturing = true
	s.mu.Unlock()

	// The pointer of the system says where the cursor of the virtual
	// desktop begins, so that it does not begin at the corner of the
	// screen while the mouse is somewhere else.
	if p := backend.Primary(); p.X != 0 || p.Y != 0 {
		s.mu.Lock()
		s.cursorX, s.cursorY = p.X, p.Y
		s.cursorKnown = true
		s.mu.Unlock()
	}

	if err := backend.Capture(s.onLocalEvent); err != nil {
		s.mu.Lock()
		s.capturing = false
		s.mu.Unlock()
		return err
	}
	return nil
}

// onLocalEvent takes an event of the mouse of this device.
func (s *Session) onLocalEvent(e input.Event) {
	s.mu.Lock()
	mode := s.mode
	h := s.hub
	c := s.client
	s.mu.Unlock()

	switch mode {
	case ModeHost:
		if h == nil {
			return
		}
		switch e.Kind {
		case input.KindMove:
			// The cursor of this device is tracked on its own screen,
			// and the pointer of the virtual desktop follows it.
			x, y := s.trackCursor(e)
			h.LocalMove(x, y)
		case input.KindButton:
			h.LocalEvent(protocol.EventButton, 0, 0, protocol.Button(e.Button), e.Down, 0)
		case input.KindWheel:
			h.LocalEvent(protocol.EventWheel, 0, 0, 0, false, e.Wheel)
		}
	case ModeClient:
		if c == nil {
			return
		}
		switch e.Kind {
		case input.KindMove:
			// The place of the cursor of this device travels with the
			// movements, so that the host can tell where the pointer is
			// on the virtual desktop.
			x, y := s.trackCursor(e)
			_ = c.Move(e.DX, e.DY, x, y)
		case input.KindButton:
			_ = c.Button(protocol.Button(e.Button), e.Down)
		case input.KindWheel:
			_ = c.Wheel(e.Wheel)
		}
	}
}

// trackCursor follows the cursor of the mouse of this device on the screen
// of this device, and says where it is.
//
// The first movement says where the pointer is, so that the pointer of the
// virtual desktop begins at the place the pointer of the system is at, and
// not at the corner of the screen. After that the movements are followed.
//
// The pointer of the system cannot leave the screen, so the place is kept
// inside it.
func (s *Session) trackCursor(e input.Event) (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.cursorKnown {
		s.cursorX, s.cursorY = e.X, e.Y
		s.cursorKnown = true
	} else {
		s.cursorX += e.DX
		s.cursorY += e.DY
	}

	width, height := s.screenWidthLocked(), s.screenHeightLocked()
	if width > 0 {
		if s.cursorX < 0 {
			s.cursorX = 0
		}
		if s.cursorX > width-1 {
			s.cursorX = width - 1
		}
	}
	if height > 0 {
		if s.cursorY < 0 {
			s.cursorY = 0
		}
		if s.cursorY > height-1 {
			s.cursorY = height - 1
		}
	}
	return s.cursorX, s.cursorY
}

// onRemoteEvent takes an event that the hub sent.
func (s *Session) onRemoteEvent(m protocol.Message) {
	s.mu.Lock()
	backend := s.backend
	s.mu.Unlock()
	if backend == nil {
		return
	}
	switch m.Kind {
	case protocol.EventMove:
		_ = backend.Move(m.DX, m.DY)
	case protocol.EventButton:
		_ = backend.Button(input.Button(m.Button), m.Down)
	case protocol.EventWheel:
		_ = backend.Wheel(m.Wheel)
	}
}

// onRemotePointer takes the place of the pointer that the hub sent.
func (s *Session) onRemotePointer(device, active string, x, y int) {
	s.mu.Lock()
	s.controller = device
	s.active = active
	s.pointer = virtual.Point{X: x, Y: y}
	backend := s.backend
	own := s.ownRect()
	s.mu.Unlock()

	// The pointer is shown where it falls on the screen of this device,
	// or at the edge of it when it falls outside.
	if backend != nil && own != nil {
		local := own.Local(virtual.Point{X: x, Y: y})
		_ = backend.MoveTo(local.X, local.Y)
		s.mu.Lock()
		s.cursorX, s.cursorY = local.X, local.Y
		s.mu.Unlock()
	}
	s.notify()
}

// onRemoteWarp takes the place where the cursor of this device has to go,
// so that the pointer can go on past the edge of the screen.
func (s *Session) onRemoteWarp(x, y int) {
	s.mu.Lock()
	backend := s.backend
	s.cursorX, s.cursorY = x, y
	s.mu.Unlock()
	if backend != nil {
		_ = backend.MoveTo(x, y)
	}
}

// onRemoteDevices takes the list of devices that the hub sent.
func (s *Session) onRemoteDevices(devices []protocol.DeviceInfo) {
	s.mu.Lock()
	s.devices = devices
	s.mu.Unlock()
	s.notify()
}

// ownRect returns the place of the screen of this device in the virtual
// desktop, if the hub told the session about it.
func (s *Session) ownRect() *virtual.Rect {
	for _, d := range s.devices {
		if d.Self {
			return &virtual.Rect{
				X:      d.X,
				Y:      d.Y,
				Width:  d.Width,
				Height: d.Height,
			}
		}
	}
	return nil
}

// screenWidth returns the width of the screen of this device.
func (s *Session) screenWidth() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.screenWidthLocked()
}

// screenWidthLocked returns the width of the screen of this device. The
// lock of the session must be held.
func (s *Session) screenWidthLocked() int {
	if s.backend == nil {
		return 0
	}
	screens := s.backend.Screens()
	if len(screens) == 0 {
		return 0
	}
	return screens[0].Width
}

// screenHeight returns the height of the screen of this device.
func (s *Session) screenHeight() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.screenHeightLocked()
}

// screenHeightLocked returns the height of the screen of this device. The
// lock of the session must be held.
func (s *Session) screenHeightLocked() int {
	if s.backend == nil {
		return 0
	}
	screens := s.backend.Screens()
	if len(screens) == 0 {
		return 0
	}
	return screens[0].Height
}

// Name returns the name of this device.
func (s *Session) Name() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.name
}

// Ping keeps the connection to the hub alive.
func (s *Session) Ping() {
	s.mu.Lock()
	c := s.client
	s.mu.Unlock()
	if c != nil {
		_ = c.Send(protocol.Message{Type: protocol.TypePing})
	}
}

// KeepAlive pings the hub now and then, until the session stops.
func (s *Session) KeepAlive(stop <-chan struct{}) {
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			s.Ping()
		}
	}
}
