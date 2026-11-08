// Package virtual holds the geometry of the virtual desktop the devices
// share: where each screen sits, and where the pointer is on it.
//
// The virtual desktop has no size of its own. It is the union of the
// screens of the devices, laid out around the screen of the host: a device
// that joins on the right edge of the host sits to its right, and the
// pointer that walks off the right edge of the host walks onto it.
package virtual

import (
	"sort"

	"mmouseshare/internal/protocol"
)

// Point is a position in the virtual desktop.
type Point struct {
	X int
	Y int
}

// Rect is a rectangle in the virtual desktop.
type Rect struct {
	X      int
	Y      int
	Width  int
	Height int
}

// Contains tells whether p is inside r.
func (r Rect) Contains(p Point) bool {
	return p.X >= r.X && p.X < r.X+r.Width && p.Y >= r.Y && p.Y < r.Y+r.Height
}

// Clamp returns the point of r that is the closest to p, so that a pointer
// that left a screen sticks to its edge instead of disappearing. The point
// is in the place of the virtual desktop.
func (r Rect) Clamp(p Point) Point {
	x := p.X
	if x < r.X {
		x = r.X
	}
	if x > r.X+r.Width-1 {
		x = r.X + r.Width - 1
	}
	y := p.Y
	if y < r.Y {
		y = r.Y
	}
	if y > r.Y+r.Height-1 {
		y = r.Y + r.Height - 1
	}
	return Point{X: x, Y: y}
}

// Local maps a point of the virtual desktop to a point of the screen that
// sits at the place of the rectangle. The point is clamped to the size of
// the screen, so that a pointer that left the virtual desktop sticks to
// the edge of it.
func (r Rect) Local(p Point) Point {
	x := p.X - r.X
	y := p.Y - r.Y
	if x < 0 {
		x = 0
	}
	if x > r.Width-1 {
		x = r.Width - 1
	}
	if y < 0 {
		y = 0
	}
	if y > r.Height-1 {
		y = r.Height - 1
	}
	return Point{X: x, Y: y}
}

// Device is one screen of the virtual desktop.
type Device struct {
	// ID identifies the device among the ones of the host.
	ID string
	// Name is the name the device gave.
	Name string
	// Edge is where the device sits, relative to the host.
	Edge protocol.Edge
	// Rect is the place of the screen in the virtual desktop.
	Rect Rect
	// Width and Height are the size of the screen, in pixels.
	Width  int
	Height int
	// Self tells that this is the device of the process itself.
	Self bool
	// Online tells that the device is connected.
	Online bool
}

// Local maps a point of the virtual desktop to a point of the screen of the
// device. The point is clamped to the screen, so that a pointer that left
// the virtual desktop sticks to the edge of the screen.
func (d Device) Local(p Point) Point {
	return d.Rect.Local(p)
}

// Virtual maps a point of the screen of the device to a point of the
// virtual desktop.
func (d Device) Virtual(p Point) Point {
	return Point{X: p.X + d.Rect.X, Y: p.Y + d.Rect.Y}
}

// Layout is the place of every screen of the virtual desktop.
type Layout struct {
	// Host is the screen of the host, the origin of the virtual desktop.
	Host Device
	// Peers are the screens of the other devices, in the order they
	// joined.
	Peers []Device
}

// Devices returns every device of the layout, the host first.
func (l *Layout) Devices() []Device {
	out := make([]Device, 0, len(l.Peers)+1)
	out = append(out, l.Host)
	out = append(out, l.Peers...)
	return out
}

// Device returns the device with the given ID.
func (l *Layout) Device(id string) (Device, bool) {
	if l.Host.ID == id {
		return l.Host, true
	}
	for _, d := range l.Peers {
		if d.ID == id {
			return d, true
		}
	}
	return Device{}, false
}

// Build lays the screens out around the host.
//
// The host sits at the origin. The peers on the right edge follow it, one
// after the other, the peers on the left edge are mirrored, and the peers
// on the top and the bottom edges are stacked above and below the host.
func Build(host Device, peers []Device) *Layout {
	l := &Layout{Host: host}
	if host.Width <= 0 {
		host.Width = 1
	}
	if host.Height <= 0 {
		host.Height = 1
	}
	// The host sits at the origin of the virtual desktop.
	host.Rect = Rect{X: 0, Y: 0, Width: host.Width, Height: host.Height}
	l.Host = host

	// The peers on each edge, in the order they joined.
	byEdge := map[protocol.Edge][]Device{}
	for _, p := range peers {
		byEdge[p.Edge] = append(byEdge[p.Edge], p)
	}

	// Right: one after the other, to the right of the host.
	x := host.Width
	for _, p := range byEdge[protocol.EdgeRight] {
		p.Rect = Rect{X: x, Y: 0, Width: p.Width, Height: p.Height}
		l.Peers = append(l.Peers, p)
		x += p.Width
	}

	// Left: the peer that joined last is the one next to the host.
	x = 0
	for i := len(byEdge[protocol.EdgeLeft]) - 1; i >= 0; i-- {
		p := byEdge[protocol.EdgeLeft][i]
		x += p.Width
		p.Rect = Rect{X: -x, Y: 0, Width: p.Width, Height: p.Height}
		l.Peers = append(l.Peers, p)
	}

	// Top: stacked above the host.
	y := 0
	for _, p := range byEdge[protocol.EdgeTop] {
		y += p.Height
		p.Rect = Rect{X: 0, Y: -y, Width: p.Width, Height: p.Height}
		l.Peers = append(l.Peers, p)
	}

	// Bottom: stacked below the host.
	y = 0
	for _, p := range byEdge[protocol.EdgeBottom] {
		p.Rect = Rect{X: 0, Y: host.Height + y, Width: p.Width, Height: p.Height}
		l.Peers = append(l.Peers, p)
		y += p.Height
	}

	return l
}

// At returns the device whose screen holds the point, and the point itself.
func (l *Layout) At(p Point) (Device, Point, bool) {
	for _, d := range l.Devices() {
		if d.Rect.Contains(p) {
			return d, p, true
		}
	}
	return Device{}, p, false
}

// Neighbor returns the device that shares an edge with the given one, on
// the side the pointer left the screen on.
//
// The pointer that walks off the right edge of a screen lands on the
// screen that starts where that edge ends, so the devices are looked up by
// the place they share rather than by the edge they joined on.
func (l *Layout) Neighbor(from Device, p Point) (Device, bool) {
	candidates := l.Devices()
	for _, d := range candidates {
		if d.ID == from.ID {
			continue
		}
		switch {
		case p.X >= from.Rect.X+from.Rect.Width && d.Rect.X == from.Rect.X+from.Rect.Width &&
			overlap(d.Rect.Y, d.Rect.Y+d.Rect.Height, from.Rect.Y, from.Rect.Y+from.Rect.Height):
			return d, true
		case p.X < from.Rect.X && d.Rect.X+d.Rect.Width == from.Rect.X &&
			overlap(d.Rect.Y, d.Rect.Y+d.Rect.Height, from.Rect.Y, from.Rect.Y+from.Rect.Height):
			return d, true
		case p.Y >= from.Rect.Y+from.Rect.Height && d.Rect.Y == from.Rect.Y+from.Rect.Height &&
			overlap(d.Rect.X, d.Rect.X+d.Rect.Width, from.Rect.X, from.Rect.X+from.Rect.Width):
			return d, true
		case p.Y < from.Rect.Y && d.Rect.Y+d.Rect.Height == from.Rect.Y &&
			overlap(d.Rect.X, d.Rect.X+d.Rect.Width, from.Rect.X, from.Rect.X+from.Rect.Width):
			return d, true
		}
	}
	return Device{}, false
}

// overlap tells whether the intervals a and b share a point.
func overlap(a0, a1, b0, b1 int) bool {
	return a0 < b1 && b0 < a1
}

// HasNeighbor tells whether a screen of the layout has another one next
// to it on the given side.
//
// The sides are the ones of the host: right, left, top and bottom.
func (l *Layout) HasNeighbor(id string, dir protocol.Edge) bool {
	from, ok := l.Device(id)
	if !ok {
		return false
	}
	switch dir {
	case protocol.EdgeRight:
		return l.anyRect(func(d Device) bool {
			return d.ID != from.ID && d.Rect.X == from.Rect.X+from.Rect.Width &&
				overlap(d.Rect.Y, d.Rect.Y+d.Rect.Height, from.Rect.Y, from.Rect.Y+from.Rect.Height)
		})
	case protocol.EdgeLeft:
		return l.anyRect(func(d Device) bool {
			return d.ID != from.ID && d.Rect.X+d.Rect.Width == from.Rect.X &&
				overlap(d.Rect.Y, d.Rect.Y+d.Rect.Height, from.Rect.Y, from.Rect.Y+from.Rect.Height)
		})
	case protocol.EdgeBottom:
		return l.anyRect(func(d Device) bool {
			return d.ID != from.ID && d.Rect.Y == from.Rect.Y+from.Rect.Height &&
				overlap(d.Rect.X, d.Rect.X+d.Rect.Width, from.Rect.X, from.Rect.X+from.Rect.Width)
		})
	case protocol.EdgeTop:
		return l.anyRect(func(d Device) bool {
			return d.ID != from.ID && d.Rect.Y+d.Rect.Height == from.Rect.Y &&
				overlap(d.Rect.X, d.Rect.X+d.Rect.Width, from.Rect.X, from.Rect.X+from.Rect.Width)
		})
	}
	return false
}

// anyRect tells whether any screen of the layout matches.
func (l *Layout) anyRect(f func(Device) bool) bool {
	for _, d := range l.Devices() {
		if f(d) {
			return true
		}
	}
	return false
}

// SortByName orders the devices by their name, for a stable list in the
// interface.
func SortByName(devices []Device) {
	sort.SliceStable(devices, func(i, j int) bool {
		return devices[i].Name < devices[j].Name
	})
}
