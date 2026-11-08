package hub

import (
	"bufio"
	"net"
	"testing"
	"time"

	"mmouseshare/internal/protocol"
	"mmouseshare/internal/virtual"
)

// waitFor reads the messages of a client until one of the kind wanted
// comes, or the time runs out.
func waitFor(t *testing.T, r *bufio.Reader, kind protocol.MessageType) protocol.Message {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := protocol.Read(r)
		if err != nil {
			t.Fatalf("reading the message: %v", err)
		}
		if msg.Type == kind {
			return msg
		}
	}
	t.Fatalf("no %s message came", kind)
	return protocol.Message{}
}

// connect joins a hub as a client and returns the reader of the messages.
func connect(t *testing.T, h *Hub, name string, edge protocol.Edge, width, height int) *bufio.Reader {
	t.Helper()
	conn, err := net.Dial("tcp", addrOf(t, h))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if err := protocol.Write(rw, protocol.Message{
		Type:    protocol.TypeHello,
		Version: protocol.Version,
		Name:    name,
		Edge:    edge,
		Width:   width,
		Height:  height,
	}); err != nil {
		t.Fatal(err)
	}
	msg, err := protocol.Read(rw.Reader)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Type != protocol.TypeWelcome {
		t.Fatalf("the hub answered %s, want a welcome", msg.Type)
	}
	return rw.Reader
}

// addrOf returns the address the hub listens on.
func addrOf(t *testing.T, h *Hub) string {
	t.Helper()
	return "127.0.0.1:" + itoa(h.Port())
}

// itoa turns a number into a string.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TestHubAcceptsAClient makes sure that a client is taken and told about
// the devices.
func TestHubAcceptsAClient(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	r := connect(t, h, "client", protocol.EdgeRight, 1280, 1024)

	msg := waitFor(t, r, protocol.TypeDevices)
	if len(msg.Devices) != 2 {
		t.Fatalf("the hub knows %d devices, want 2", len(msg.Devices))
	}
	var found bool
	for _, d := range msg.Devices {
		if d.ID == "client" {
			found = true
			if d.Width != 1280 || d.Height != 1024 {
				t.Errorf("the screen of the client is %dx%d, want 1280x1024", d.Width, d.Height)
			}
		}
	}
	if !found {
		t.Error("the client is not in the list of the devices")
	}
}

// TestThePointerCrossesToTheDeviceOnTheRight makes sure that the pointer
// that walks off the right edge of the host shows up on the device that
// sits there, without a jump.
func TestThePointerCrossesToTheDeviceOnTheRight(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	r := connect(t, h, "client", protocol.EdgeRight, 1280, 1024)
	// The list of the devices comes first.
	waitFor(t, r, protocol.TypeDevices)

	// The warp of the cursor of the host is told to the host itself.
	var warpX, warpY int
	h.SetLocalWarp(func(x, y int) { warpX, warpY = x, y })

	// The pointer walks to the right edge of the host.
	h.LocalMove(1919, 100)

	msg := waitFor(t, r, protocol.TypePointer)
	if msg.Active != "host" {
		t.Errorf("the pointer is on %s, want host", msg.Active)
	}
	// The cursor of the host is warped to the other edge of its screen,
	// so that the mouse can go on.
	if warpX != 0 || warpY != 100 {
		t.Errorf("the cursor goes to %d,%d, want 0,100", warpX, warpY)
	}

	// The pointer goes on to the screen of the client.
	h.LocalMove(1, 100)

	msg = waitFor(t, r, protocol.TypePointer)
	if msg.Active != "client" {
		t.Errorf("the pointer is on %s, want client", msg.Active)
	}
	// The pointer did not jump: it went on where it was.
	if msg.X != 1920 {
		t.Errorf("the pointer is at %d, want 1920", msg.X)
	}
}

// TestTheButtonGoesToTheScreenThePointerIsOn makes sure that a click
// reaches the device whose screen the pointer is on, and not the one that
// holds the mouse.
func TestTheButtonGoesToTheScreenThePointerIsOn(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	r := connect(t, h, "client", protocol.EdgeRight, 1280, 1024)
	waitFor(t, r, protocol.TypeDevices)

	// The pointer walks onto the screen of the client.
	h.LocalMove(1919, 100)
	waitFor(t, r, protocol.TypePointer)
	h.LocalMove(1, 100)
	waitFor(t, r, protocol.TypePointer)

	// The click that follows belongs to the client.
	h.LocalEvent(protocol.EventButton, 0, 0, protocol.ButtonLeft, true, 0)

	msg := waitFor(t, r, protocol.TypeEvent)
	if msg.Kind != protocol.EventButton {
		t.Fatalf("the event is %s, want a button", msg.Kind)
	}
	if msg.Button != protocol.ButtonLeft || !msg.Down {
		t.Errorf("the event is %+v, want the left button down", msg)
	}
}

// TestTheMouseOfTheClientTakesThePointerOver makes sure that the mouse of
// a client takes the pointer over when the user moves it, and that the
// pointer lands on the place of the client.
func TestTheMouseOfTheClientTakesThePointerOver(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	r := connect(t, h, "client", protocol.EdgeRight, 1280, 1024)
	waitFor(t, r, protocol.TypeDevices)

	// The mouse of the client moves, so the client takes the pointer.
	// The cursor of the client was at 100,200 on its own screen, and it
	// moved by 5,7, so it is at 105,207 now. The screen of the client
	// sits at 1920,0 on the virtual desktop.
	h.handleEvent(h.peers["client"], protocol.Message{
		Type: protocol.TypeEvent,
		Kind: protocol.EventMove,
		DX:   5,
		DY:   7,
		X:    105,
		Y:    207,
	})

	if h.Controller() != "client" {
		t.Errorf("the pointer is held by %s, want client", h.Controller())
	}
	p := h.Pointer()
	if p.X != 1920+100+5 || p.Y != 200+7 {
		t.Errorf("the pointer is at %d,%d, want %d,%d",
			p.X, p.Y, 1920+100+5, 200+7)
	}
	msg := waitFor(t, r, protocol.TypePointer)
	if msg.Device != "client" {
		t.Errorf("the hub says %s holds the pointer, want client", msg.Device)
	}
}

// TestThePointerStaysOnTheVirtualDesktop makes sure that a pointer that
// walks off the whole desktop comes back instead of disappearing.
func TestThePointerStaysOnTheVirtualDesktop(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	h.LocalMove(-5, 100)

	p := h.Pointer()
	if p.X < 0 {
		t.Errorf("the pointer is at %d, want it on the screen", p.X)
	}
	if h.Active() != "host" {
		t.Errorf("the pointer is on %s, want host", h.Active())
	}
}
// TestTheButtonOfTheClientKeepsThePlaceOfThePointer makes sure that a
// click of a client does not move the pointer, because a click says
// nothing about the place of the cursor.
func TestTheButtonOfTheClientKeepsThePlaceOfThePointer(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	r := connect(t, h, "client", protocol.EdgeRight, 1280, 1024)
	waitFor(t, r, protocol.TypeDevices)

	// The mouse of the client moves, so the client holds the pointer and
	// the pointer sits at 1920+100,200.
	h.handleEvent(h.peers["client"], protocol.Message{
		Type: protocol.TypeEvent,
		Kind: protocol.EventMove,
		DX:   5,
		DY:   7,
		X:    105,
		Y:    207,
	})
	waitFor(t, r, protocol.TypePointer)

	// The click of the client comes without a place, so the place that is
	// known stays.
	h.handleEvent(h.peers["client"], protocol.Message{
		Type:   protocol.TypeEvent,
		Kind:   protocol.EventButton,
		Button: protocol.ButtonLeft,
		Down:   true,
	})

	p := h.Pointer()
	if p.X != 1920+105 || p.Y != 207 {
		t.Errorf("the pointer is at %d,%d, want %d,%d",
			p.X, p.Y, 1920+105, 207)
	}
}

// TestTheLayoutPutsTheClientOnTheEdgeItAskedFor makes sure that the
// device sits where it asked to sit.
func TestTheLayoutPutsTheClientOnTheEdgeItAskedFor(t *testing.T) {
	h, err := New("Host", "host", 0)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		t.Fatal(err)
	}

	connect(t, h, "client", protocol.EdgeLeft, 1280, 1024)

	h.mu.Lock()
	d, ok := h.layout.Device("client")
	h.mu.Unlock()
	if !ok {
		t.Fatal("the client is not in the layout")
	}
	if d.Rect.X+d.Rect.Width != 0 {
		t.Errorf("the client ends at %d, want it at the left of the host",
			d.Rect.X+d.Rect.Width)
	}
}

var _ = virtual.Point{}
