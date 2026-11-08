// Package hub is the server of the sharing: it holds the virtual desktop,
// tells the devices where they sit, and moves the pointer between them.
//
// One pointer is shared by every device. It lives in the virtual desktop,
// the place where the screens of the devices are laid out next to each
// other. Each device shows the pointer where it falls on its own screen,
// so that a pointer that walks off the right edge of one screen shows up
// on the screen next to it.
//
// The device whose mouse the user is moving is the one that holds the
// pointer. When the user moves the mouse of another device, that device
// takes the pointer over, so the mouse of whichever computer is under the
// hand drives the other ones.
//
// The mouse of the operating system cannot leave the screen it is on, so
// when the pointer walks to the edge of the screen of the device that
// holds it, the cursor of that device is warped to the other edge of the
// screen, and the pointer goes on. The pointer never jumps, because the
// place of the screen in the virtual desktop moves by the same amount as
// the cursor.
package hub

import (
	"bufio"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"mmouseshare/internal/protocol"
	"mmouseshare/internal/virtual"
)

// The defaults of the hub.
const (
	// DefaultPort is the port the hub listens on.
	DefaultPort = 27301
	// writeTimeout is how long a message waits for a client before the
	// client is taken as gone. Without it a client that stopped reading
	// holds the writer, and with it the mouse of the host, for good.
	writeTimeout = 2 * time.Second
)

// Hub is the server of the sharing.
type Hub struct {
	mu sync.Mutex

	// name is the name of this device.
	name string
	// id identifies this device.
	id string
	// port is the port the hub listens on.
	port int

	// host is the screen of this device.
	host virtual.Device
	// peers are the screens of the other devices.
	peers map[string]*peer
	// order keeps the peers in the order they joined.
	order []string

	// layout is the place of every screen.
	layout *virtual.Layout

	// controller is the device whose mouse holds the pointer.
	controller string
	// cursorX and cursorY are the place of the cursor of the host, on
	// its own screen.
	cursorX int
	cursorY int
	// localWarp moves the cursor of the mouse of the host.
	localWarp func(x, y int)
	// originX and originY are the place of the screen of the
	// controller in the virtual desktop.
	originX int
	originY int
	// pointer is where the pointer is, in the virtual desktop.
	pointer virtual.Point
	// active is the screen the pointer is on.
	active string

	listener net.Listener
	closed   bool
}

// peer is a device that is connected to the hub.
type peer struct {
	id     string
	name   string
	edge   protocol.Edge
	width  int
	height int

	conn net.Conn
	rw   *bufio.ReadWriter
	// mu keeps the messages whole: the mouse of the host, the reader of
	// this client and the walk of the pointer all write to it, and a
	// message has to go out in one piece.
	mu sync.Mutex

	// x and y are the place of the cursor of the peer, on its own
	// screen.
	x int
	y int

	online bool
	seen   time.Time
}

// New returns a hub that listens on the given port. A port of 0 lets the
// system choose one.
func New(name, id string, port int) (*Hub, error) {
	return &Hub{
		name:  name,
		id:    id,
		port:  port,
		peers: map[string]*peer{},
	}, nil
}

// SetLocalWarp tells the hub how to move the cursor of the mouse of this
// device, which it does when the pointer walks off the edge of the screen.
func (h *Hub) SetLocalWarp(fn func(x, y int)) {
	h.mu.Lock()
	h.localWarp = fn
	h.mu.Unlock()
}

// SetHostScreen tells the hub the size of the screen of this device.
func (h *Hub) SetHostScreen(width, height int) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.host = virtual.Device{
		ID:     h.id,
		Name:   h.name,
		Edge:   protocol.EdgeRight,
		Width:  width,
		Height: height,
		Self:   true,
		Online: true,
	}
	if h.controller == "" {
		h.controller = h.id
	}
	h.rebuildLocked()
}

// Port returns the port the hub listens on.
func (h *Hub) Port() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.port
}

// Listen starts accepting the clients.
func (h *Hub) Listen() error {
	ln, err := net.Listen("tcp", fmt.Sprintf(":%d", h.port))
	if err != nil {
		return fmt.Errorf("hub: listen: %w", err)
	}
	h.mu.Lock()
	h.listener = ln
	h.port = ln.Addr().(*net.TCPAddr).Port
	h.mu.Unlock()

	go h.accept(ln)
	return nil
}

// accept takes the clients as they come.
func (h *Hub) accept(ln net.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			h.mu.Lock()
			closed := h.closed
			h.mu.Unlock()
			if closed {
				return
			}
			continue
		}
		go h.serve(conn)
	}
}

// serve talks to one client.
func (h *Hub) serve(conn net.Conn) {
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))

	// The first message of a client says what it is.
	msg, err := protocol.Read(rw.Reader)
	if err != nil {
		return
	}
	if msg.Type != protocol.TypeHello {
		_ = protocol.Write(rw, protocol.Message{
			Type:    protocol.TypeError,
			Message: "hub: the first message must be a hello",
		})
		return
	}
	if msg.Version != 0 && msg.Version != protocol.Version {
		_ = protocol.Write(rw, protocol.Message{
			Type:    protocol.TypeError,
			Message: fmt.Sprintf("hub: the protocol version %d does not match %d", msg.Version, protocol.Version),
		})
		return
	}

	id := msg.Name
	if id == "" {
		id = fmt.Sprintf("device-%d", time.Now().UnixNano()%10000)
	}
	edge := msg.Edge
	if !edge.Valid() {
		edge = protocol.EdgeRight
	}

	p := &peer{
		id:     id,
		name:   msg.Name,
		edge:   edge,
		width:  msg.Width,
		height: msg.Height,
		conn:   conn,
		rw:     rw,
		online: true,
		seen:   time.Now(),
	}

	h.mu.Lock()
	h.peers[p.id] = p
	h.order = append(h.order, p.id)
	h.rebuildLocked()
	h.mu.Unlock()

	// The client is told that it is welcome before it hears about the
	// devices, so that it knows the hub took it.
	if err := p.write(protocol.Message{
		Type:    protocol.TypeWelcome,
		Version: protocol.Version,
		Port:    h.port,
	}); err != nil {
		h.remove(p.id)
		return
	}

	h.broadcastDevices()

	defer h.remove(p.id)

	for {
		msg, err := protocol.Read(rw.Reader)
		if err != nil {
			return
		}
		h.handle(p, msg)
	}
}

// remove forgets a client that is gone.
func (h *Hub) remove(id string) {
	h.mu.Lock()
	if _, ok := h.peers[id]; !ok {
		h.mu.Unlock()
		return
	}
	delete(h.peers, id)
	for i, v := range h.order {
		if v == id {
			h.order = append(h.order[:i], h.order[i+1:]...)
			break
		}
	}
	if h.controller == id {
		// The device that held the pointer is gone, so the pointer
		// comes back to the host.
		h.controller = h.id
		h.originX, h.originY = h.host.Rect.X, h.host.Rect.Y
	}
	h.rebuildLocked()
	h.mu.Unlock()
	h.broadcastDevices()
}

// handle reads one message of a client.
func (h *Hub) handle(p *peer, msg protocol.Message) {
	h.mu.Lock()
	p.seen = time.Now()
	h.mu.Unlock()

	switch msg.Type {
	case protocol.TypeEvent:
		h.handleEvent(p, msg)
	case protocol.TypeGrab:
		h.grab(p)
	case protocol.TypeRegister:
		h.mu.Lock()
		if msg.Edge.Valid() {
			p.edge = msg.Edge
		}
		if msg.Width > 0 {
			p.width = msg.Width
		}
		if msg.Height > 0 {
			p.height = msg.Height
		}
		h.rebuildLocked()
		h.mu.Unlock()
		h.broadcastDevices()
	case protocol.TypePing:
		_ = p.write(protocol.Message{Type: protocol.TypePong})
	case protocol.TypePong:
		// Nothing to do, the client is alive.
	}
}

// handleEvent takes a mouse event of a client.
func (h *Hub) handleEvent(from *peer, msg protocol.Message) {
	h.mu.Lock()
	// Only a movement says where the cursor of the peer is. A button or a
	// wheel says nothing about the place, so it must not wipe out the
	// place that is known.
	if msg.Kind == protocol.EventMove {
		from.x = msg.X
		from.y = msg.Y
	}
	// The mouse of a device that does not hold the pointer takes the
	// pointer over, the same way the mouse of the host does.
	if h.controller != from.id {
		h.controller = from.id
		h.setOriginLocked(from.id)
	}
	// A movement says where the cursor of the peer is. A button or a
	// wheel says nothing about the place, so the place that is known is
	// used instead.
	localX, localY := from.x, from.y
	if msg.Kind == protocol.EventMove {
		localX, localY = msg.X, msg.Y
	}
	h.mu.Unlock()

	h.pointerMoved(from.id, localX, localY, msg.Kind, msg.Button, msg.Down, msg.Wheel)
}

// grab gives the pointer to a device that asks for it.
func (h *Hub) grab(p *peer) {
	h.mu.Lock()
	h.controller = p.id
	h.setOriginLocked(p.id)
	h.mu.Unlock()

	h.pointerMoved(p.id, p.x, p.y, protocol.EventMove, 0, false, 0)
}

// pointerMoved moves the pointer of the virtual desktop, and hands the
// screen over to the neighbour when the pointer walks off the edge.
//
// The cursor of the operating system cannot leave the screen it is on, so
// when the pointer reaches the edge of the screen of the device that holds
// it, the cursor of that device is warped to the other edge of the screen.
// The place of the screen in the virtual desktop moves by the same amount,
// so the pointer goes on where it was.
func (h *Hub) pointerMoved(id string, localX, localY int, kind protocol.EventKind, btn protocol.Button, down bool, wheel int) {
	h.mu.Lock()

	// The place of the screen of the device that holds the pointer. It
	// only changes when the pointer moves to another device, or when the
	// cursor is warped to the other edge of the screen.
	if h.controller != id {
		h.setOriginLocked(id)
	}
	// The pointer is the cursor of the device, in the place of its
	// screen on the virtual desktop.
	h.pointer = virtual.Point{X: h.originX + localX, Y: h.originY + localY}

	// The screen the pointer is on.
	active, _, ok := h.layout.At(h.pointer)
	if !ok {
		// The pointer walked off the virtual desktop, so it comes back
		// to the screen of the device that holds it.
		if d, found := h.layout.Device(id); found {
			local := d.Rect.Local(virtual.Point{X: localX, Y: localY})
			h.pointer = virtual.Point{X: d.Rect.X + local.X, Y: d.Rect.Y + local.Y}
		}
		active, _, _ = h.layout.At(h.pointer)
	}
	h.active = active.ID

	// When the pointer reaches the edge of the screen of the device that
	// holds it, and there is a screen next to it, the cursor of that
	// device is warped to the other edge, and the pointer goes on.
	warp := h.warpLocked(id, localX, localY)
	if warp != nil {
		// The cursor of the device that holds the pointer goes to the
		// place of the warp. The device says nothing about it, because
		// the move is made by the system and not by the user, so the
		// place is remembered here.
		if id == h.id {
			h.cursorX, h.cursorY = warp.X, warp.Y
		} else if p, ok := h.peers[id]; ok {
			p.x, p.y = warp.X, warp.Y
		}
	}

	controller := h.controller
	peers := make([]*peer, 0, len(h.peers))
	for _, pid := range h.order {
		peers = append(peers, h.peers[pid])
	}
	h.mu.Unlock()

	// The device that holds the pointer is told to move its cursor.
	if warp != nil {
		if id == h.id {
			h.warpLocal(warp.X, warp.Y)
		} else {
			for _, p := range peers {
				if p.id == id {
					_ = p.write(protocol.Message{
						Type: protocol.TypeWarp,
						X:    warp.X,
						Y:    warp.Y,
					})
					break
				}
			}
		}
	}

	// Every device shows the pointer where it falls on its own screen.
	for _, p := range peers {
		_ = p.write(protocol.Message{
			Type:   protocol.TypePointer,
			Device: controller,
			Active: active.ID,
			X:      h.pointer.X,
			Y:      h.pointer.Y,
		})
	}

	// A button or a wheel belongs to the screen the pointer is on.
	if kind == protocol.EventButton || kind == protocol.EventWheel {
		if active.ID != controller {
			for _, p := range peers {
				if p.id == active.ID {
					_ = p.write(protocol.Message{
						Type:   protocol.TypeEvent,
						Kind:   kind,
						Button: btn,
						Down:   down,
						Wheel:  wheel,
					})
					break
				}
			}
		}
	}
}

// warpLocked tells where the cursor of the device that holds the pointer
// has to go, when the pointer walks to the edge of its screen.
//
// The lock of the hub must be held.
func (h *Hub) warpLocked(id string, localX, localY int) *virtual.Point {
	d, ok := h.layout.Device(id)
	if !ok || d.Width <= 0 || d.Height <= 0 {
		return nil
	}
	switch {
	case localX >= d.Width-1 && h.layout.HasNeighbor(id, protocol.EdgeRight):
		// The pointer goes on to the right, so the cursor goes back to
		// the left of the screen. The place of the screen moves by the
		// same amount, so the pointer goes on where it was.
		h.originX += d.Width - 1
		return &virtual.Point{X: 0, Y: localY}
	case localX <= 0 && h.layout.HasNeighbor(id, protocol.EdgeLeft):
		h.originX -= d.Width - 1
		return &virtual.Point{X: d.Width - 1, Y: localY}
	case localY >= d.Height-1 && h.layout.HasNeighbor(id, protocol.EdgeBottom):
		h.originY += d.Height - 1
		return &virtual.Point{X: localX, Y: 0}
	case localY <= 0 && h.layout.HasNeighbor(id, protocol.EdgeTop):
		h.originY -= d.Height - 1
		return &virtual.Point{X: localX, Y: d.Height - 1}
	}
	return nil
}

// setOriginLocked puts the screen of a device back in its place, which is
// what happens when the pointer moves to that device. The lock of the hub
// must be held.
func (h *Hub) setOriginLocked(id string) {
	if d, ok := h.layout.Device(id); ok {
		h.originX, h.originY = d.Rect.X, d.Rect.Y
	} else {
		h.originX, h.originY = 0, 0
	}
}

// warpLocal moves the cursor of the mouse of this device.
func (h *Hub) warpLocal(x, y int) {
	if h.localWarp != nil {
		h.localWarp(x, y)
	}
}

// LocalMove moves the pointer of the virtual desktop to the place of the
// cursor of the mouse of this device.
//
// Moving the mouse of the host takes the pointer back, the same way
// moving the mouse of a client does.
func (h *Hub) LocalMove(x, y int) {
	h.mu.Lock()
	if h.controller != h.id {
		// The mouse of the host takes the pointer over.
		h.controller = h.id
		h.setOriginLocked(h.id)
	}
	h.cursorX = x
	h.cursorY = y
	h.mu.Unlock()

	h.pointerMoved(h.id, x, y, protocol.EventMove, 0, false, 0)
}

// LocalEvent takes a mouse event of the mouse of this device.
//
// The cursor of this device is tracked on its own screen, and the pointer
// of the virtual desktop follows it, so that the pointer never drifts
// away from the screen it is on.
func (h *Hub) LocalEvent(kind protocol.EventKind, dx, dy int, btn protocol.Button, down bool, wheel int) {
	h.mu.Lock()
	// The mouse of the host only holds the pointer while no other
	// device took it over.
	if h.controller != h.id {
		h.mu.Unlock()
		return
	}
	localX, localY := h.cursorX, h.cursorY
	h.mu.Unlock()

	h.pointerMoved(h.id, localX, localY, kind, btn, down, wheel)

	// The buttons and the wheel of the host go to the screen the pointer
	// is on.
	if kind == protocol.EventButton || kind == protocol.EventWheel {
		h.mu.Lock()
		active := h.active
		peers := make([]*peer, 0, len(h.peers))
		for _, pid := range h.order {
			peers = append(peers, h.peers[pid])
		}
		h.mu.Unlock()
		if active != h.id {
			for _, p := range peers {
				if p.id == active {
					_ = p.write(protocol.Message{
						Type:   protocol.TypeEvent,
						Kind:   kind,
						Button: btn,
						Down:   down,
						Wheel:  wheel,
					})
					break
				}
			}
		}
	}
}

// Active returns the screen the pointer is on.
func (h *Hub) Active() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.active
}

// Controller returns the device whose mouse holds the pointer.
func (h *Hub) Controller() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.controller
}

// Pointer returns where the pointer is, in the virtual desktop.
func (h *Hub) Pointer() virtual.Point {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.pointer
}

// Devices returns the devices of the virtual desktop.
func (h *Hub) Devices() []protocol.DeviceInfo {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.deviceInfosLocked()
}

// deviceInfosLocked describes the devices of the virtual desktop.
func (h *Hub) deviceInfosLocked() []protocol.DeviceInfo {
	out := make([]protocol.DeviceInfo, 0, len(h.peers)+1)
	out = append(out, protocol.DeviceInfo{
		Name:   h.host.Name,
		ID:     h.host.ID,
		Edge:   h.host.Edge,
		Width:  h.host.Width,
		Height: h.host.Height,
		X:      h.host.Rect.X,
		Y:      h.host.Rect.Y,
		Self:   true,
		Online: true,
		Active: h.active == h.host.ID,
	})
	for _, id := range h.order {
		p := h.peers[id]
		info := protocol.DeviceInfo{
			Name:   p.name,
			ID:     p.id,
			Edge:   p.edge,
			Width:  p.width,
			Height: p.height,
			Online: p.online,
			Active: h.active == p.id,
		}
		if d, ok := h.layout.Device(p.id); ok {
			info.X = d.Rect.X
			info.Y = d.Rect.Y
		}
		out = append(out, info)
	}
	return out
}

// rebuildLocked puts the screens back in place.
func (h *Hub) rebuildLocked() {
	peers := make([]virtual.Device, 0, len(h.peers))
	for _, id := range h.order {
		p := h.peers[id]
		peers = append(peers, virtual.Device{
			ID:     p.id,
			Name:   p.name,
			Edge:   p.edge,
			Width:  p.width,
			Height: p.height,
			Online: p.online,
		})
	}
	h.layout = virtual.Build(h.host, peers)
}

// broadcastDevices tells every client about the devices.
func (h *Hub) broadcastDevices() {
	h.mu.Lock()
	infos := h.deviceInfosLocked()
	peers := make([]*peer, 0, len(h.peers))
	for _, id := range h.order {
		peers = append(peers, h.peers[id])
	}
	h.mu.Unlock()

	msg := protocol.Message{Type: protocol.TypeDevices, Devices: infos}
	for _, p := range peers {
		if err := p.write(msg); err != nil {
			log.Println("hub:", err)
		}
	}
}

// Close stops the hub.
func (h *Hub) Close() error {
	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		return nil
	}
	h.closed = true
	ln := h.listener
	peers := make([]*peer, 0, len(h.peers))
	for _, id := range h.order {
		peers = append(peers, h.peers[id])
	}
	h.mu.Unlock()

	if ln != nil {
		_ = ln.Close()
	}
	for _, p := range peers {
		_ = p.conn.Close()
	}
	return nil
}

// write sends a message to the client, alone, and closes the connection when
// it cannot: the reader of the client then goes, and the hub forgets it
// instead of waiting for it at every movement of the mouse.
func (p *peer) write(m protocol.Message) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		_ = p.conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	}
	err := protocol.Write(p.rw, m)
	if err != nil && p.conn != nil {
		_ = p.conn.Close()
	}
	return err
}
