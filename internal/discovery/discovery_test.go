package discovery

import (
	"testing"
	"time"
)

// TestScannersShareThePort listens with two scanners at once, as the window of
// a hub and the window of a device of the same machine do.
func TestScannersShareThePort(t *testing.T) {
	first, err := NewScanner()
	if err != nil {
		t.Fatalf("the first scanner: %v", err)
	}
	defer func() { _ = first.Close() }()

	second, err := NewScanner()
	if err != nil {
		t.Fatalf("the second scanner: %v (the port of the beacons is not shared)", err)
	}
	defer func() { _ = second.Close() }()
}

// TestScannerHearsTheAnnouncer tells that a hub that announces and looks for
// the other hubs of the room at the same time finds itself, which the two
// sockets of one program have to make possible.
func TestScannerHearsTheAnnouncer(t *testing.T) {
	scanner, err := NewScanner()
	if err != nil {
		t.Fatalf("the scanner: %v", err)
	}
	defer func() { _ = scanner.Close() }()

	announcer, err := NewAnnouncer("test", 27301, 1440, 900)
	if err != nil {
		t.Fatalf("the announcer: %v", err)
	}
	defer func() { _ = announcer.Close() }()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, d := range scanner.Devices() {
			if d.Name == "test" {
				if d.Port != 27301 {
					t.Errorf("the hub said port %d, want 27301", d.Port)
				}
				return
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("the scanner did not hear the announcer, devices %+v", scanner.Devices())
}
