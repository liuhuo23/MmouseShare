// Command check runs a host and a client of MmouseShare against each
// other, to check that the sharing works end to end.
//
// It is a program of its own, so that it does not touch the mouse of the
// computer it runs on.
package main

import (
	"bufio"
	"fmt"
	"log"
	"time"

	"mmouseshare/internal/hub"
	"mmouseshare/internal/protocol"
)

// The check runs the host on a port of its own and joins it as a client.
func main() {
	if err := check(); err != nil {
		log.Fatal(err)
	}
	fmt.Println("the sharing works")
}

// check does the work.
func check() error {
	addr, h, cleanup, err := startHost()
	if err != nil {
		return err
	}
	defer cleanup()

	// The host tells whether the cursor of its own screen is of any use.
	// Only the screen that holds the pointer shows a cursor.
	var visible bool
	var active string
	h.SetLocalActive(func(a string, x, y int, v bool) {
		active, visible = a, v
	})

	conn, err := dial(addr)
	if err != nil {
		return err
	}
	defer conn.Close()

	rw := bufio.NewReadWriter(bufio.NewReader(conn), bufio.NewWriter(conn))
	if err := protocol.Write(rw, protocol.Message{
		Type:    protocol.TypeHello,
		Version: protocol.Version,
		Name:    "checker",
		Edge:    protocol.EdgeRight,
		Width:   1280,
		Height:  1024,
	}); err != nil {
		return err
	}

	msg, err := protocol.Read(rw.Reader)
	if err != nil {
		return err
	}
	if msg.Type != protocol.TypeWelcome {
		return fmt.Errorf("the host answered %s, want a welcome", msg.Type)
	}
	fmt.Println("the host welcomed the client")

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := protocol.Read(rw.Reader)
		if err != nil {
			return err
		}
		if msg.Type != protocol.TypeDevices {
			continue
		}
		for _, d := range msg.Devices {
			if d.ID == "checker" {
				if d.X == 0 {
					return fmt.Errorf("the client sits at the origin of the virtual desktop")
				}
				fmt.Printf("the client sits at %d,%d on the virtual desktop\n", d.X, d.Y)
				return checkPointer(rw, h, &visible, &active)
			}
		}
	}
	return fmt.Errorf("the host did not tell about the client")
}

// checkPointer walks the pointer onto the screen of the client and makes
// sure that the client sees it there, and that the host hides the cursor of
// its own screen while the pointer is elsewhere.
func checkPointer(rw *bufio.ReadWriter, h *hub.Hub, visible *bool, active *string) error {
	moveMouse(h)

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		msg, err := protocol.Read(rw.Reader)
		if err != nil {
			return err
		}
		if msg.Type != protocol.TypePointer {
			continue
		}
		if msg.Active != "checker" {
			continue
		}
		if msg.X != 1920 {
			return fmt.Errorf("the pointer is at %d, want it at 1920", msg.X)
		}
		fmt.Println("the pointer walks onto the screen of the client")

		if *active != "checker" {
			return fmt.Errorf("the host thinks the pointer is on %s, want it on the client", *active)
		}
		if *visible {
			return fmt.Errorf("the cursor of the host is of use, want it to be of no use")
		}
		fmt.Println("the host hides the cursor of its own screen")
		return nil
	}
	return fmt.Errorf("the pointer did not walk onto the screen of the client")
}
