// Package discovery finds the hubs of the local network.
//
// A device that runs a hub says that it is there, over UDP, every second.
// The devices that look for a hub listen to the same port, so they find
// the hubs of the room without typing any address.
package discovery

import (
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"
)

// The defaults of the discovery.
const (
	// DefaultPort is the port the beacons travel on.
	DefaultPort = 27302
	// beaconInterval is how often a hub says that it is there.
	beaconInterval = time.Second
	// expiry is how long a beacon of a hub is trusted.
	expiry = 5 * time.Second
	// broadcastAddr is where the beacons go.
	broadcastAddr = "255.255.255.255"
)

// Beacon is what a hub says about itself.
type Beacon struct {
	// Name is the name of the device.
	Name string `json:"name"`
	// Port is the port the hub listens on.
	Port int `json:"port"`
	// Address is the address of the hub, as the device saw it.
	Address string `json:"address"`
	// Width and Height are the size of the screen of the device.
	Width  int `json:"width"`
	Height int `json:"height"`
}

// Device is a hub that was found on the network.
type Device struct {
	// Beacon is what the hub said about itself.
	Beacon
	// Address is the address to connect to.
	Address string
	// LastSeen is when the hub was heard from.
	LastSeen time.Time
}

// Announcer says that a hub is there.
type Announcer struct {
	mu sync.Mutex

	beacon Beacon
	conn   net.PacketConn
	closed bool
	done   chan struct{}
}

// NewAnnouncer returns an announcer that says that a hub is there. The
// beacon is sent on the given port.
func NewAnnouncer(name string, port, width, height int) (*Announcer, error) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", DefaultPort))
	if err != nil {
		return nil, fmt.Errorf("discovery: listen: %w", err)
	}
	a := &Announcer{
		beacon: Beacon{Name: name, Port: port, Width: width, Height: height},
		conn:   conn,
		done:   make(chan struct{}),
	}
	go a.run()
	return a, nil
}

// run sends the beacon every second.
func (a *Announcer) run() {
	ticker := time.NewTicker(beaconInterval)
	defer ticker.Stop()
	for {
		a.send()
		select {
		case <-a.done:
			return
		case <-ticker.C:
		}
	}
}

// send sends the beacon once.
func (a *Announcer) send() {
	a.mu.Lock()
	beacon := a.beacon
	conn := a.conn
	closed := a.closed
	a.mu.Unlock()
	if closed || conn == nil {
		return
	}
	data, err := json.Marshal(beacon)
	if err != nil {
		return
	}
	addr := &net.UDPAddr{IP: net.IPv4bcast, Port: DefaultPort}
	_, _ = conn.WriteTo(data, addr)
}

// Close stops the announcer.
func (a *Announcer) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	conn := a.conn
	close(a.done)
	a.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}

// Scanner looks for the hubs of the local network.
type Scanner struct {
	mu sync.Mutex

	conn    net.PacketConn
	devices map[string]*Device
	onFound func(Device)
	closed  bool
	done    chan struct{}
}

// NewScanner returns a scanner that listens for the beacons of the hubs.
func NewScanner() (*Scanner, error) {
	conn, err := net.ListenPacket("udp4", fmt.Sprintf(":%d", DefaultPort))
	if err != nil {
		return nil, fmt.Errorf("discovery: listen: %w", err)
	}
	s := &Scanner{
		conn:    conn,
		devices: map[string]*Device{},
		done:    make(chan struct{}),
	}
	go s.run()
	return s, nil
}

// OnFound sets the function called when a hub is found.
func (s *Scanner) OnFound(fn func(Device)) {
	s.mu.Lock()
	s.onFound = fn
	s.mu.Unlock()
}

// run takes the beacons that come.
func (s *Scanner) run() {
	buf := make([]byte, 4096)
	for {
		n, addr, err := s.conn.ReadFrom(buf)
		if err != nil {
			return
		}
		var beacon Beacon
		if err := json.Unmarshal(buf[:n], &beacon); err != nil {
			continue
		}
		if beacon.Port == 0 {
			continue
		}
		host, _, err := net.SplitHostPort(addr.String())
		if err != nil {
			continue
		}
		beacon.Address = host

		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return
		}
		key := fmt.Sprintf("%s:%d", host, beacon.Port)
		device := Device{
			Beacon:   beacon,
			Address:  key,
			LastSeen: time.Now(),
		}
		s.devices[key] = &device
		onFound := s.onFound
		s.mu.Unlock()

		if onFound != nil {
			onFound(device)
		}
	}
}

// Devices returns the hubs that were found, the freshest first.
func (s *Scanner) Devices() []Device {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Device, 0, len(s.devices))
	for _, d := range s.devices {
		if time.Since(d.LastSeen) > expiry {
			continue
		}
		out = append(out, *d)
	}
	return out
}

// Close stops the scanner.
func (s *Scanner) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	conn := s.conn
	close(s.done)
	s.mu.Unlock()
	if conn != nil {
		return conn.Close()
	}
	return nil
}
