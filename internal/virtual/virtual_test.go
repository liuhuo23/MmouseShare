package virtual

import (
	"testing"

	"mmouseshare/internal/protocol"
)

// hostDevice is the screen of the host of the tests.
func hostDevice() Device {
	return Device{ID: "host", Name: "Host", Width: 1920, Height: 1080}
}

// TestBuildPlacesTheRightEdgeAfterTheHost makes sure that a device on the
// right edge sits right of the host, and that the ones that follow sit
// after it.
func TestBuildPlacesTheRightEdgeAfterTheHost(t *testing.T) {
	right := Device{ID: "right", Edge: protocol.EdgeRight, Width: 1280, Height: 1024}
	other := Device{ID: "other", Edge: protocol.EdgeRight, Width: 800, Height: 600}

	l := Build(hostDevice(), []Device{right, other})

	first, ok := l.Device("right")
	if !ok {
		t.Fatal("the device on the right is missing")
	}
	if first.Rect.X != 1920 {
		t.Errorf("the device on the right starts at %d, want 1920", first.Rect.X)
	}
	second, ok := l.Device("other")
	if !ok {
		t.Fatal("the second device on the right is missing")
	}
	if second.Rect.X != 1920+1280 {
		t.Errorf("the second device on the right starts at %d, want %d", second.Rect.X, 1920+1280)
	}
}

// TestBuildPlacesTheLeftEdgeBeforeTheHost makes sure that a device on the
// left edge sits left of the host, and that the last one that joined is
// the one next to the host.
func TestBuildPlacesTheLeftEdgeBeforeTheHost(t *testing.T) {
	first := Device{ID: "a", Edge: protocol.EdgeLeft, Width: 1024, Height: 768}
	second := Device{ID: "b", Edge: protocol.EdgeLeft, Width: 1024, Height: 768}

	l := Build(hostDevice(), []Device{first, second})

	nearest, ok := l.Device("b")
	if !ok {
		t.Fatal("the device on the left is missing")
	}
	if nearest.Rect.X+nearest.Rect.Width != 0 {
		t.Errorf("the nearest device on the left ends at %d, want 0",
			nearest.Rect.X+nearest.Rect.Width)
	}
	far, ok := l.Device("a")
	if !ok {
		t.Fatal("the far device on the left is missing")
	}
	if far.Rect.X+far.Rect.Width != -1024 {
		t.Errorf("the far device on the left ends at %d, want -1024", far.Rect.X+far.Rect.Width)
	}
}

// TestAtFindsTheScreenOfThePoint makes sure that the pointer is found on
// the screen it falls on.
func TestAtFindsTheScreenOfThePoint(t *testing.T) {
	right := Device{ID: "right", Edge: protocol.EdgeRight, Width: 1280, Height: 1024}
	l := Build(hostDevice(), []Device{right})

	d, p, ok := l.At(Point{X: 100, Y: 100})
	if !ok {
		t.Fatal("the pointer on the host was not found")
	}
	if d.ID != "host" {
		t.Errorf("the pointer is on %s, want host", d.ID)
	}
	if p.X != 100 || p.Y != 100 {
		t.Errorf("the pointer is at %d,%d, want 100,100", p.X, p.Y)
	}

	d, _, ok = l.At(Point{X: 2000, Y: 100})
	if !ok {
		t.Fatal("the pointer on the device on the right was not found")
	}
	if d.ID != "right" {
		t.Errorf("the pointer is on %s, want right", d.ID)
	}
}

// TestNeighborFindsTheScreenAcrossTheEdge makes sure that the pointer
// that walks off the right edge of a screen finds the screen next to it.
func TestNeighborFindsTheScreenAcrossTheEdge(t *testing.T) {
	right := Device{ID: "right", Edge: protocol.EdgeRight, Width: 1280, Height: 1024}
	l := Build(hostDevice(), []Device{right})

	host, _ := l.Device("host")
	d, ok := l.Neighbor(host, Point{X: 1920, Y: 100})
	if !ok {
		t.Fatal("the neighbour on the right was not found")
	}
	if d.ID != "right" {
		t.Errorf("the neighbour is %s, want right", d.ID)
	}

	// Nothing is on the left of the host, so there is no neighbour.
	if _, ok := l.Neighbor(host, Point{X: -1, Y: 100}); ok {
		t.Error("a neighbour on the left was found, but there is none")
	}
}

// TestLocalClampsToTheScreen makes sure that a pointer that falls outside
// the screen sticks to its edge, so that it never disappears.
func TestLocalClampsToTheScreen(t *testing.T) {
	d := Device{ID: "right", Edge: protocol.EdgeRight, Width: 1280, Height: 1024}
	d.Rect = Rect{X: 1920, Y: 0, Width: 1280, Height: 1024}

	p := d.Local(Point{X: 1900, Y: 100})
	if p.X != 0 {
		t.Errorf("the pointer is at %d on the screen, want 0", p.X)
	}
	p = d.Local(Point{X: 4000, Y: 100})
	if p.X != 1279 {
		t.Errorf("the pointer is at %d on the screen, want 1279", p.X)
	}
}

// TestVirtualMapsAScreenToTheDesktop makes sure that the place of the
// cursor of a screen is the place of the pointer on the virtual desktop.
func TestVirtualMapsAScreenToTheDesktop(t *testing.T) {
	d := Device{ID: "right", Edge: protocol.EdgeRight, Width: 1280, Height: 1024}
	d.Rect = Rect{X: 1920, Y: 0, Width: 1280, Height: 1024}

	p := d.Virtual(Point{X: 10, Y: 20})
	if p.X != 1930 || p.Y != 20 {
		t.Errorf("the pointer is at %d,%d on the desktop, want 1930,20", p.X, p.Y)
	}
}
