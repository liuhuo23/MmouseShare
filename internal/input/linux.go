//go:build linux

package input

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/ebitengine/purego"
)

// The types of the events of the kernel input.
const (
	evSyn = 0
	evKey = 1
	evRel = 2
	evAbs = 3
)

// The relative axes of the kernel input.
const (
	relX      = 0
	relY      = 1
	relHWheel = 6
	relWheel  = 8
)

// The absolute axes of the kernel input.
const (
	absX = 0
	absY = 1
)

// The buttons of the kernel input.
const (
	btnLeft   = 0x110
	btnRight  = 0x111
	btnMiddle = 0x112
)

// The sizes of one struct input_event of the kernel. The struct starts with
// a struct timeval, which holds two long values, 4 bytes each on a 32 bit
// system and 8 bytes each on a 64 bit one, and continues with a uint16 for
// the type, a uint16 for the code and an int32 for the value.
const (
	eventHeaderSize = (bits.UintSize / 8) * 2
	eventSize       = eventHeaderSize + 8
)

// wheelUnit is the number of units the kernel reports for one turn of a
// wheel that has no high resolution.
const wheelUnit = 120

// The bits of the event types in the mask of a device of the kernel input.
const (
	evMaskRel = 1 << evRel
	evMaskAbs = 1 << evAbs
)

// The buttons of the mouse in the X11, where the wheel is a button too.
const (
	xButtonLeft   = 1
	xButtonMiddle = 2
	xButtonRight  = 3
	xButtonUp     = 4
	xButtonDown   = 5
)

// init gives the program the backend of Linux.
func init() {
	Factory = newLinuxBackend
}

// newLinuxBackend builds the backend of Linux. It opens the X display of the
// computer, which the injection needs, and fails when there is none.
func newLinuxBackend() (Backend, error) {
	x, err := openXlib()
	if err != nil {
		return nil, err
	}
	return &linuxBackend{x: x}, nil
}

// linuxBackend captures the mouse of the computer with the devices of the
// kernel and moves the pointer with the XTest extension of the X11.
//
// The capture and the injection live in the same type, because the two
// halves of the sharing never happen at the same time in one computer: the
// one that owns the mouse captures it, the other one injects it.
type linuxBackend struct {
	// x is the X display of the computer.
	x *xlib

	// mu guards the session of the capture.
	mu sync.Mutex
	// sess is the capture that runs, if any.
	sess *captureSession

	// xmu guards the calls to the X11.
	xmu sync.Mutex
}

// Capture starts reading the mouse of the computer and calls onEvent for
// every event, until Stop is called.
func (b *linuxBackend) Capture(onEvent func(Event)) error {
	if onEvent == nil {
		return errors.New("input: the capture needs a callback")
	}
	devices, err := openMouseDevices()
	if err != nil {
		return err
	}
	sess := &captureSession{
		onEvent: onEvent,
		stop:    make(chan struct{}),
		events:  make(chan Event, 256),
		done:    make(chan struct{}),
	}
	for _, d := range devices {
		sess.files = append(sess.files, d.file)
	}

	b.mu.Lock()
	if b.sess != nil {
		b.mu.Unlock()
		closeFiles(sess.files)
		return errors.New("input: the capture is already running")
	}
	b.sess = sess
	b.mu.Unlock()

	for _, d := range devices {
		sess.wg.Add(1)
		go func(d *mouseDevice) {
			defer sess.wg.Done()
			readDevice(d, sess)
		}(d)
	}
	go sess.dispatch()
	return nil
}

// Stop stops reading the mouse. It does nothing when the capture does not
// run, so that it can be called more than once.
func (b *linuxBackend) Stop() error {
	b.mu.Lock()
	sess := b.sess
	b.sess = nil
	b.mu.Unlock()
	if sess == nil {
		return nil
	}

	// The flag keeps the dispatcher from calling the callback again, the
	// channel wakes the readers of the devices up.
	sess.stopped.Store(true)
	close(sess.stop)
	closeFiles(sess.files)
	sess.wg.Wait()
	close(sess.events)

	// The dispatcher ends after the last event. Waiting for it makes sure
	// that no callback runs after Stop returns. When Stop is called by the
	// callback itself the dispatcher cannot end yet, so it is not waited
	// for.
	if sess.callbackID.Load() != goroutineID() {
		<-sess.done
	}
	return nil
}

// Move moves the pointer of the computer by a number of pixels.
func (b *linuxBackend) Move(dx, dy int) error {
	b.xmu.Lock()
	defer b.xmu.Unlock()
	if b.x.fakeRelative(b.x.display, dx, dy, 0) == 0 {
		return errors.New("input: the X11 server refused the move")
	}
	b.x.flush(b.x.display)
	return nil
}

// MoveTo puts the pointer of the computer at a place of the screen.
func (b *linuxBackend) MoveTo(x, y int) error {
	b.xmu.Lock()
	defer b.xmu.Unlock()
	if b.x.fakeMotion(b.x.display, b.x.screen, x, y, 0) == 0 {
		return errors.New("input: the X11 server refused the move")
	}
	b.x.flush(b.x.display)
	return nil
}

// Button presses or releases a button of the computer.
func (b *linuxBackend) Button(button Button, down bool) error {
	num, ok := xButton(button)
	if !ok {
		return fmt.Errorf("input: the button %d is not a mouse button", button)
	}
	b.xmu.Lock()
	defer b.xmu.Unlock()
	press := 0
	if down {
		press = 1
	}
	if b.x.fakeButton(b.x.display, num, press, 0) == 0 {
		return errors.New("input: the X11 server refused the button")
	}
	b.x.flush(b.x.display)
	return nil
}

// Wheel turns the wheel of the computer. The X11 has no wheel, so it is
// turned with the buttons 4 and 5.
func (b *linuxBackend) Wheel(notches int) error {
	if notches == 0 {
		return nil
	}
	num := uintptr(xButtonUp)
	if notches < 0 {
		num = xButtonDown
	}
	count := notches
	if count < 0 {
		count = -count
	}

	b.xmu.Lock()
	defer b.xmu.Unlock()
	for i := 0; i < count; i++ {
		if b.x.fakeButton(b.x.display, num, 1, 0) == 0 {
			return errors.New("input: the X11 server refused the wheel")
		}
		if b.x.fakeButton(b.x.display, num, 0, 0) == 0 {
			return errors.New("input: the X11 server refused the wheel")
		}
	}
	b.x.flush(b.x.display)
	return nil
}

// Screens returns the screens of the computer. The backend knows the screen
// of the X11, which is the whole desktop of the computer.
func (b *linuxBackend) Screens() []Screen {
	return []Screen{{X: 0, Y: 0, Width: b.x.width, Height: b.x.height}}
}

// Primary returns the screen with the pointer on it.
func (b *linuxBackend) Primary() Screen {
	var root, child uintptr
	var rootX, rootY, winX, winY int32
	var mask uint32

	b.xmu.Lock()
	found := b.x.queryPointer(b.x.display, b.x.defaultRoot(b.x.display),
		&root, &child, &rootX, &rootY, &winX, &winY, &mask)
	b.xmu.Unlock()

	if found == 0 {
		return Screen{}
	}
	return Screen{X: int(rootX), Y: int(rootY), Width: b.x.width, Height: b.x.height}
}

// xButton returns the number of a button of the mouse in the X11.
func xButton(button Button) (uintptr, bool) {
	switch button {
	case ButtonLeft:
		return xButtonLeft, true
	case ButtonMiddle:
		return xButtonMiddle, true
	case ButtonRight:
		return xButtonRight, true
	}
	return 0, false
}

// captureSession is a run of the capture. It holds the callback, the
// goroutines that read the devices and the events they give.
type captureSession struct {
	// onEvent is the callback of the capture.
	onEvent func(Event)
	// stop is closed when the capture stops.
	stop chan struct{}
	// events carries the events of the devices to the dispatcher.
	events chan Event
	// done is closed when the dispatcher ends.
	done chan struct{}
	// stopped tells the dispatcher to call the callback no more.
	stopped atomic.Bool
	// callbackID is the identifier of the goroutine that calls the
	// callback, which tells whether Stop is called by the callback itself.
	callbackID atomic.Uint64
	// wg waits for the readers of the devices.
	wg sync.WaitGroup
	// files are the devices of the capture, which are closed on stop.
	files []*os.File
}

// dispatch calls the callback for every event of the devices, until the
// session stops.
func (s *captureSession) dispatch() {
	defer close(s.done)
	s.callbackID.Store(goroutineID())
	for {
		select {
		case <-s.stop:
			return
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			if s.stopped.Load() {
				return
			}
			s.onEvent(ev)
		}
	}
}

// send gives an event to the dispatcher of the session.
func (s *captureSession) send(ev Event) {
	select {
	case s.events <- ev:
	case <-s.stop:
	}
}

// mouseDevice is a device of the kernel input that looks like a mouse.
type mouseDevice struct {
	// path is the file of the device.
	path string
	// name is the name of the device.
	name string
	// file is the open device.
	file *os.File
}

// openMouseDevices opens the devices of the kernel input that look like a
// mouse. The devices that cannot be opened are left alone, so that a mouse
// that needs no right does not keep the others from working.
func openMouseDevices() ([]*mouseDevice, error) {
	infos, err := readInputDevices("/proc/bus/input/devices")
	if err == nil {
		var devices []*mouseDevice
		for _, info := range infos {
			if !isMouseInfo(info) {
				continue
			}
			for _, node := range eventNodes(info.handlers) {
				dev, err := openDevice(filepath.Join("/dev/input", node), info.name)
				if err != nil {
					continue
				}
				devices = append(devices, dev)
			}
		}
		if len(devices) > 0 {
			return devices, nil
		}
	}
	return sniffMouseDevices()
}

// inputDeviceInfo is one device of the list of the kernel input.
type inputDeviceInfo struct {
	// name is the name of the device.
	name string
	// handlers are the handlers of the device, like "event3 mouse1".
	handlers string
	// evMask is the mask of the types of events the device gives.
	evMask uint64
	// hasEV tells the mask is known.
	hasEV bool
}

// readInputDevices reads the list of the devices of the kernel input. The
// file tells the name of every device and the handlers of it, which give
// the files of the events of the device.
func readInputDevices(path string) ([]inputDeviceInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var infos []inputDeviceInfo
	var cur inputDeviceInfo
	flush := func() {
		if cur.name != "" || cur.handlers != "" {
			infos = append(infos, cur)
		}
		cur = inputDeviceInfo{}
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			flush()
			continue
		}
		switch {
		case strings.HasPrefix(line, "N: Name="):
			cur.name = strings.Trim(strings.TrimPrefix(line, "N: Name="), `"`)
		case strings.HasPrefix(line, "H: Handlers="):
			cur.handlers = strings.TrimSpace(strings.TrimPrefix(line, "H: Handlers="))
		case strings.HasPrefix(line, "B: EV="):
			mask, err := strconv.ParseUint(strings.TrimSpace(strings.TrimPrefix(line, "B: EV=")), 16, 64)
			if err == nil {
				cur.evMask = mask
				cur.hasEV = true
			}
		}
	}
	flush()
	return infos, nil
}

// isMouseInfo tells whether a device of the kernel input is a mouse. A
// mouse gives relative movements and no absolute position, a touchpad gives
// both, and the virtual mouse of the X server would give back what the
// program injects, so it is left alone.
func isMouseInfo(info inputDeviceInfo) bool {
	name := strings.ToLower(info.name)
	if strings.Contains(name, "virtual core") || strings.Contains(name, "xtest") {
		return false
	}
	if info.hasEV {
		if info.evMask&evMaskRel == 0 {
			return false
		}
		if info.evMask&evMaskAbs != 0 {
			return false
		}
		return true
	}
	return strings.Contains(name, "mouse") ||
		strings.Contains(name, "trackpad") ||
		strings.Contains(name, "touchpad") ||
		strings.Contains(name, "pointer") ||
		strings.Contains(name, "trackball")
}

// eventNodes returns the files of the events of a device, which its
// handlers hold.
func eventNodes(handlers string) []string {
	var nodes []string
	for _, field := range strings.Fields(handlers) {
		if strings.HasPrefix(field, "event") {
			nodes = append(nodes, field)
		}
	}
	return nodes
}

// openDevice opens a device of the kernel input without waiting for it.
func openDevice(path, name string) (*mouseDevice, error) {
	f, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, fmt.Errorf("input: cannot open the mouse device %s: %w", path, err)
	}
	return &mouseDevice{path: path, name: name, file: f}, nil
}

// sniffMouseDevices looks for the mouse among the devices of the kernel
// input when the list of them is not readable. A device is a mouse when it
// gives relative movements and no absolute position.
func sniffMouseDevices() ([]*mouseDevice, error) {
	paths, err := filepath.Glob("/dev/input/event*")
	if err != nil || len(paths) == 0 {
		return nil, errors.New("input: no mouse device found in /dev/input")
	}
	var devices []*mouseDevice
	for _, path := range paths {
		dev, err := openDevice(path, filepath.Base(path))
		if err != nil {
			continue
		}
		rel, abs := sniffDevice(dev.file)
		if abs || !rel {
			dev.file.Close()
			continue
		}
		devices = append(devices, dev)
	}
	if len(devices) == 0 {
		return nil, errors.New("input: no mouse device found in /dev/input, the user may need the right to read it")
	}
	return devices, nil
}

// sniffDevice reads the events of a device for a moment and tells whether
// it gives relative movements and whether it gives an absolute position.
func sniffDevice(f *os.File) (hasRel, hasAbs bool) {
	deadline := time.Now().Add(150 * time.Millisecond)
	buf := make([]byte, eventSize*16)
	for time.Now().Before(deadline) {
		n, err := f.Read(buf)
		for i := 0; i+eventSize <= n; i += eventSize {
			typ, _, _ := parseInputEvent(buf[i:])
			switch typ {
			case evRel:
				hasRel = true
			case evAbs:
				hasAbs = true
			}
		}
		if hasRel && hasAbs {
			break
		}
		if err != nil && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	return hasRel, hasAbs
}

// readDevice reads the events of one device and gives them to the session,
// until the device is closed or the session stops.
//
// The kernel gives the movements of the mouse and not the place of the
// pointer on the screen, so the position of the events is a position of the
// own counting, which starts at the place where the pointer was when the
// capture began.
func readDevice(dev *mouseDevice, sess *captureSession) {
	ep, err := syscall.EpollCreate1(syscall.EPOLL_CLOEXEC)
	if err != nil {
		ep = -1
	} else {
		event := syscall.EpollEvent{Fd: int32(dev.file.Fd()), Events: syscall.EPOLLIN}
		if err := syscall.EpollCtl(ep, syscall.EPOLL_CTL_ADD, int(dev.file.Fd()), &event); err != nil {
			syscall.Close(ep)
			ep = -1
		}
	}
	if ep >= 0 {
		defer syscall.Close(ep)
	}

	// The buffer holds the events that are not whole at the end of a read.
	buf := make([]byte, 4096+eventSize)
	raw := make([]byte, 4096)
	size := 0

	var dx, dy int
	var x, y int
	var wheel int
	for {
		n, err := dev.file.Read(raw)
		if n > 0 {
			size += copy(buf[size:], raw[:n])
			for size >= eventSize {
				typ, code, value := parseInputEvent(buf)

				switch typ {
				case evKey:
					if button, ok := kernelButton(code); ok {
						sess.send(Event{Kind: KindButton, Button: button, Down: value != 0, X: x, Y: y})
					}
				case evRel:
					switch code {
					case relX:
						dx += int(value)
					case relY:
						dy += int(value)
					case relWheel:
						wheel += wheelNotches(value)
					case relHWheel:
						// The horizontal wheel has no place in the events of
						// the program, so it is left alone.
					}
				case evAbs:
					switch code {
					case absX:
						x = int(value)
					case absY:
						y = int(value)
					}
				case evSyn:
					// Only the report of a frame ends a movement.
					if code == 0 {
						if dx != 0 || dy != 0 {
							x += dx
							y += dy
							sess.send(Event{Kind: KindMove, DX: dx, DY: dy, X: x, Y: y})
							dx, dy = 0, 0
						}
						if wheel != 0 {
							sess.send(Event{Kind: KindWheel, Wheel: wheel, X: x, Y: y})
							wheel = 0
						}
					}
				}
				// The event that was read leaves the front of the buffer.
				copy(buf, buf[eventSize:size])
				size -= eventSize
			}
		}
		if err != nil {
			if errors.Is(err, syscall.EAGAIN) || errors.Is(err, syscall.EWOULDBLOCK) {
				if !waitReadable(ep, sess) {
					return
				}
				continue
			}
			// The device is closed or the read failed, there is nothing
			// more to read.
			return
		}
	}
}

// waitReadable waits until the device has an event to read. It returns
// false when the session stops before that.
func waitReadable(ep int, sess *captureSession) bool {
	if ep < 0 {
		select {
		case <-sess.stop:
			return false
		case <-time.After(2 * time.Millisecond):
			return true
		}
	}
	events := make([]syscall.EpollEvent, 1)
	for {
		n, err := syscall.EpollWait(ep, events, 50)
		if err != nil {
			if errors.Is(err, syscall.EINTR) {
				continue
			}
			return false
		}
		if n > 0 {
			return true
		}
		select {
		case <-sess.stop:
			return false
		default:
		}
	}
}

// parseInputEvent reads one struct input_event from the start of b.
func parseInputEvent(b []byte) (typ uint16, code uint16, value int32) {
	typ = binary.LittleEndian.Uint16(b[eventHeaderSize:])
	code = binary.LittleEndian.Uint16(b[eventHeaderSize+2:])
	value = int32(binary.LittleEndian.Uint32(b[eventHeaderSize+4:]))
	return typ, code, value
}

// kernelButton returns the button of the mouse that a code of the kernel
// input holds.
func kernelButton(code uint16) (Button, bool) {
	switch code {
	case btnLeft:
		return ButtonLeft, true
	case btnMiddle:
		return ButtonMiddle, true
	case btnRight:
		return ButtonRight, true
	}
	return 0, false
}

// wheelNotches turns a value of the wheel of the kernel input into a number
// of notches. A mouse without high resolution gives 120 units for one turn,
// a mouse with high resolution gives less.
func wheelNotches(value int32) int {
	v := int(value)
	if v == 0 {
		return 0
	}
	if v >= wheelUnit || v <= -wheelUnit {
		n := v / wheelUnit
		if n == 0 {
			// A turn that is a bit more than half a notch is a notch.
			n = 1
			if v < 0 {
				n = -1
			}
		}
		return n
	}
	return v
}

// closeFiles closes the devices of a capture.
func closeFiles(files []*os.File) {
	for _, f := range files {
		f.Close()
	}
}

// goroutineID returns the identifier of the calling goroutine. It tells
// whether Stop is called by the callback of the capture itself.
func goroutineID() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	// The first line of the stack is "goroutine 12 [running]:".
	fields := strings.Fields(string(buf[:n]))
	if len(fields) < 2 {
		return 0
	}
	id, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0
	}
	return id
}

// xlib holds the functions of the X11 and of the XTest library that the
// backend calls to move the pointer of the computer.
type xlib struct {
	// display is the connection with the X server.
	display uintptr
	// screen is the screen of the X server the pointer moves on.
	screen int
	// width and height are the size of the screen, in pixels.
	width  int
	height int

	// The functions of the X11 and of the XTest library. The ones that the
	// backend does not call yet are bound all the same, so that the
	// program can reach them.
	openDisplay   func(*byte) uintptr
	closeDisplay  func(uintptr) int
	defaultScreen func(uintptr) int
	displayWidth  func(uintptr, int) int
	displayHeight func(uintptr, int) int
	defaultRoot   func(uintptr) uintptr
	flush         func(uintptr) int
	fakeMotion    func(uintptr, int, int, int, uintptr) int
	fakeRelative  func(uintptr, int, int, uintptr) int
	fakeButton    func(uintptr, uintptr, int, uintptr) int
	queryPointer  func(uintptr, uintptr, *uintptr, *uintptr, *int32, *int32, *int32, *int32, *uint32) int32
}

// openXlib loads the X11 and the XTest library and opens the display of the
// computer.
func openXlib() (*xlib, error) {
	x11, err := purego.Dlopen("libX11.so.6", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("input: cannot load libX11.so.6: %w", err)
	}
	xtst, err := purego.Dlopen("libXtst.so.6", purego.RTLD_LAZY|purego.RTLD_GLOBAL)
	if err != nil {
		return nil, fmt.Errorf("input: cannot load libXtst.so.6: %w", err)
	}
	// The functions of the XTest live in libXtst and the other ones in
	// libX11, but some distributions put them together, so every symbol is
	// looked for in the two libraries.
	libs := []uintptr{x11, xtst}

	x := &xlib{}
	if err := bind(libs, "XOpenDisplay", &x.openDisplay); err != nil {
		return nil, err
	}
	if err := bind(libs, "XCloseDisplay", &x.closeDisplay); err != nil {
		return nil, err
	}
	if err := bind(libs, "XDefaultScreen", &x.defaultScreen); err != nil {
		return nil, err
	}
	if err := bind(libs, "XDisplayWidth", &x.displayWidth); err != nil {
		return nil, err
	}
	if err := bind(libs, "XDisplayHeight", &x.displayHeight); err != nil {
		return nil, err
	}
	if err := bind(libs, "XDefaultRootWindow", &x.defaultRoot); err != nil {
		return nil, err
	}
	if err := bind(libs, "XFlush", &x.flush); err != nil {
		return nil, err
	}
	if err := bind(libs, "XTestFakeMotionEvent", &x.fakeMotion); err != nil {
		return nil, err
	}
	if err := bind(libs, "XTestFakeRelativeMotionEvent", &x.fakeRelative); err != nil {
		return nil, err
	}
	if err := bind(libs, "XTestFakeButtonEvent", &x.fakeButton); err != nil {
		return nil, err
	}
	if err := bind(libs, "XQueryPointer", &x.queryPointer); err != nil {
		return nil, err
	}

	// A nil name makes the X11 use the DISPLAY of the environment.
	display := x.openDisplay(nil)
	if display == 0 {
		return nil, errors.New("input: cannot open the X display, MmouseShare needs X11 or XWayland on Linux")
	}
	x.display = display
	x.screen = x.defaultScreen(display)
	x.width = x.displayWidth(display, x.screen)
	x.height = x.displayHeight(display, x.screen)
	return x, nil
}

// bind looks a symbol up in the libraries and registers it in the pointer.
func bind[T any](libs []uintptr, name string, target *T) error {
	addr, err := findSymbol(libs, name)
	if err != nil {
		return err
	}
	purego.RegisterFunc(target, addr)
	return nil
}

// findSymbol looks a symbol up in the libraries, in the order they are
// given.
func findSymbol(libs []uintptr, name string) (uintptr, error) {
	for _, lib := range libs {
		if addr, err := purego.Dlsym(lib, name); err == nil && addr != 0 {
			return addr, nil
		}
	}
	return 0, fmt.Errorf("input: the symbol %s is not in the X11 libraries", name)
}
