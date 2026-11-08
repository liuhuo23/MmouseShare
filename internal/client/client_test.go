package client

import (
	"bufio"
	"net"
	"sync"
	"testing"
	"time"

	"mmouseshare/internal/protocol"
)

// newPipedClient returns a client that writes into one end of a pipe, and the
// other end, as the hub sees it.
func newPipedClient() (*Client, net.Conn) {
	here, there := net.Pipe()
	c := &Client{
		conn: here,
		rw:   bufio.NewReadWriter(bufio.NewReader(here), bufio.NewWriter(here)),
		done: make(chan struct{}),
	}
	return c, there
}

// TestSendKeepsMessagesWhole writes from many goroutines at once, as the
// window, the mouse of the computer and the keep alive do, and reads the
// messages the hub would get.
func TestSendKeepsMessagesWhole(t *testing.T) {
	c, there := newPipedClient()
	defer func() { _ = there.Close() }()

	const writers, each = 8, 50
	read := make(chan int, 1)
	go func() {
		r := bufio.NewReader(there)
		n := 0
		for {
			if _, err := protocol.Read(r); err != nil {
				read <- n
				return
			}
			n++
		}
	}()

	var wg sync.WaitGroup
	errc := make(chan error, writers)
	for i := 0; i < writers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < each; j++ {
				if err := c.Send(protocol.Message{
					Type: protocol.TypeEvent,
					Kind: protocol.EventMove,
					DX:   1,
					DY:   2,
					X:    300,
					Y:    400,
				}); err != nil {
					errc <- err
					return
				}
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		t.Errorf("a message did not go out: %v", err)
	}

	_ = there.Close()
	select {
	case n := <-read:
		if want := writers * each; n != want {
			t.Errorf("the hub read %d messages, want %d: the messages are mixed up", n, want)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the hub read nothing")
	}
}

// TestSendGivesUpOnADeadHub tells that a hub that is gone fails the message,
// so that the reader of the client can say so in the window.
func TestSendGivesUpOnADeadHub(t *testing.T) {
	c, there := newPipedClient()
	_ = there.Close()

	if err := c.Send(protocol.Message{Type: protocol.TypePing}); err == nil {
		t.Fatal("the message to a hub that is gone did not fail")
	}
}

// TestSendGivesUpOnAHubThatDoesNotRead tells that a hub that stopped reading
// does not hold the caller for good: the window of the program writes to the
// connection too.
func TestSendGivesUpOnAHubThatDoesNotRead(t *testing.T) {
	c, there := newPipedClient()
	var once sync.Once
	defer once.Do(func() { _ = there.Close() })

	done := make(chan error, 1)
	go func() { done <- c.Send(protocol.Message{Type: protocol.TypePing}) }()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the message to a hub that does not read did not fail")
		}
	case <-time.After(writeTimeout + 3*time.Second):
		t.Fatal("the message waited for the hub for good")
	}
	_ = there.Close()
}
