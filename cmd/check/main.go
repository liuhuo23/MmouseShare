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
	addr, cleanup, err := startHost()
	if err != nil {
		return err
	}
	defer cleanup()

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
				return nil
			}
		}
	}
	return fmt.Errorf("the host did not tell about the client")
}
