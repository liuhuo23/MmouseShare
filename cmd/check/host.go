package main

import (
	"fmt"
	"net"
	"time"

	"mmouseshare/internal/hub"
	"mmouseshare/internal/protocol"
)

// startHost runs a hub of its own and returns the address it listens on.
func startHost() (string, func(), error) {
	h, err := hub.New("Host", "host", 0)
	if err != nil {
		return "", nil, err
	}
	h.SetHostScreen(1920, 1080)
	if err := h.Listen(); err != nil {
		return "", nil, err
	}
	// The hub keeps the pointer on the host until a client takes it.
	go func() {
		for {
			time.Sleep(200 * time.Millisecond)
			h.LocalEvent(protocol.EventMove, 1, 0, 0, false, 0)
		}
	}()
	return fmt.Sprintf("127.0.0.1:%d", h.Port()), func() { _ = h.Close() }, nil
}

// dial joins a hub.
func dial(addr string) (net.Conn, error) {
	return net.DialTimeout("tcp", addr, 5*time.Second)
}
