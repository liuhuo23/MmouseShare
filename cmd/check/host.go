package main

import (
	"fmt"
	"net"
	"time"

	"mmouseshare/internal/hub"
)

// startHost runs a hub of its own and returns the address it listens on,
// together with the hub itself, so that the check can move its mouse.
func startHost() (string, *hub.Hub, func(), error) {
	h, err := hub.New("Host", "host", 0)
	if err != nil {
		return "", nil, nil, err
	}
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		return "", nil, nil, err
	}
	return fmt.Sprintf("127.0.0.1:%d", h.Port()), h, func() { _ = h.Close() }, nil
}

// dial joins a hub.
func dial(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, 5*time.Second)
}

// moveMouse walks the mouse of the host to the right, until the pointer
// leaves the screen of the host and lands on the screen of the client.
func moveMouse(h *hub.Hub) {
	// The cursor of the host reaches the right edge of its screen, and the
	// system warps it to the other edge, so that the mouse can go on.
	h.LocalMove(0, 0, 1919, 100)
	// The mouse goes on, and the pointer walks onto the screen of the
	// client.
	h.LocalMove(1, 0, 1, 100)
}
