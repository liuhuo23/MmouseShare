//go:build darwin

package input

// The backend of macOS.
//
// The mouse is read with an event tap of the window server, and it is moved
// with the events the window server posts. The functions of CoreGraphics and
// of CoreFoundation are called by hand, with purego, so that the program
// stays free of cgo and can be built for every computer of the network.
//
// The tap of the mouse needs the permission of the accessibility, which the
// user gives to the program in the settings of the system.

import (
	"errors"
	"fmt"
	"runtime"
	"sync"

	"github.com/ebitengine/purego"
)

// The types of the events of the window server.
const (
	cgEventLeftMouseDown     = 1
	cgEventLeftMouseUp       = 2
	cgEventRightMouseDown    = 3
	cgEventRightMouseUp      = 4
	cgEventMouseMoved        = 5
	cgEventLeftMouseDragged  = 6
	cgEventRightMouseDragged = 7
	cgEventScrollWheel       = 22
	cgEventOtherMouseDown    = 25
	cgEventOtherMouseUp      = 26
	cgEventOtherMouseDragged = 27

	// The tap is turned off when it takes too long or when the user asks
	// for it.
	cgEventTapDisabledByTimeout   = 0xFFFFFFFE
	cgEventTapDisabledByUserInput = 0xFFFFFFFF
)

// The fields of an event.
const (
	cgFieldMouseButtonNumber       = 3
	cgFieldMouseDeltaX             = 4
	cgFieldMouseDeltaY             = 5
	cgFieldScrollDeltaAxis1        = 11
	cgFieldSourceUserData          = 42
	cgFieldScrollIsContinuous      = 88
	cgFieldScrollFixedPtDeltaAxis1 = 93
	cgFieldScrollPointDeltaAxis1   = 96

	// cgFieldDirectionInvertedFromDevice tells that the system turned the
	// direction of the device the other way round, which it does when the
	// natural scrolling is on.
	cgFieldDirectionInvertedFromDevice = 137
)

// The places of a tap and the ways it works.
const (
	cgHIDEventTap        = 0
	cgHeadInsertEventTap = 0
	// The tap only listens, it never holds an event back.
	cgEventTapOptionListenOnly = 1
)

// The state of a source of events.
const cgEventSourceStateHIDSystemState = 1

// The units of a scroll wheel event.
const cgScrollEventUnitLine = 1

// The buttons of a mouse.
const (
	cgMouseButtonLeft   = 0
	cgMouseButtonRight  = 1
	cgMouseButtonCenter = 2
)

// The encoding of a string of CoreFoundation.
const cfStringEncodingUTF8 = 0x08000100

// cgUserDataMagic is the mark of the events the program sends itself, so
// that they do not come back to it.
const cgUserDataMagic = 0x4D4D5348

// cgEventMask is the mask of the events the tap asks for.
const cgEventMask = 1<<cgEventLeftMouseDown |
	1<<cgEventLeftMouseUp |
	1<<cgEventRightMouseDown |
	1<<cgEventRightMouseUp |
	1<<cgEventMouseMoved |
	1<<cgEventLeftMouseDragged |
	1<<cgEventRightMouseDragged |
	1<<cgEventOtherMouseDown |
	1<<cgEventOtherMouseUp |
	1<<cgEventOtherMouseDragged |
	1<<cgEventScrollWheel

// cgPoint is a CGPoint of CoreGraphics.
type cgPoint struct {
	X float64
	Y float64
}

// cgRect is a CGRect of CoreGraphics.
type cgRect struct {
	X      float64
	Y      float64
	Width  float64
	Height float64
}

// The functions of CoreGraphics and of CoreFoundation that the backend
// calls, bound by hand so that the program stays free of cgo.
var (
	cfLib uintptr

	cgEventCreate               func(source uintptr) uintptr
	cgEventCreateMouseEvent     func(source uintptr, typ uint32, at cgPoint, button uint32) uintptr
	cgEventCreateScrollWheel    func(source uintptr, units, count uint32, w1, w2, w3 int32) uintptr
	cgEventPost                 func(tap uint32, event uintptr)
	cgEventGetLocation          func(event uintptr) cgPoint
	cgEventGetIntegerValueField func(event uintptr, field uint32) int64
	cgEventSetIntegerValueField func(event uintptr, field uint32, value int64)
	cgEventSourceCreate         func(state int32) uintptr
	cgEventSourceSetUserData    func(source uintptr, data int64)
	cgEventTapCreate            func(tap, place, options uint32, mask uintptr, callback uintptr, userInfo uintptr) uintptr
	cgEventTapEnable            func(port uintptr, enable bool)
	cgPreflightPostEventAccess  func() bool
	cgRequestPostEventAccess    func() bool
	cgGetActiveDisplayList      func(max uint32, displays *uint32, count *uint32) int32
	cgDisplayPixelsWide         func(display uint32) int32
	cgDisplayPixelsHigh         func(display uint32) int32
	cgDisplayBounds             func(display uint32) cgRect
	cgMainDisplayID             func() uint32
	cgDisplayHideCursor         func(display uint32) bool
	cgDisplayShowCursor         func(display uint32) bool

	cfRelease                     func(uintptr)
	cfRunLoopGetCurrent           func() uintptr
	cfRunLoopAddSource            func(loop, source, mode uintptr)
	cfRunLoopRemoveSource         func(loop, source, mode uintptr)
	cfRunLoopRun                  func()
	cfRunLoopStop                 func(loop uintptr)
	cfMachPortCreateRunLoopSource func(alloc, port uintptr, order int32) uintptr
	cfStringCreateWithCString     func(alloc uintptr, text *byte, encoding uint32) uintptr
)

// loadOnce makes sure that the libraries are loaded only once.
var loadOnce sync.Once

// loadErr is the error of the loading of the libraries.
var loadErr error

// loadCoreGraphics opens the frameworks of the window server and binds the
// functions the backend calls.
func loadCoreGraphics() error {
	loadOnce.Do(func() {
		loadErr = bindCoreGraphics()
	})
	return loadErr
}

// bindCoreGraphics opens the frameworks and binds the functions.
func bindCoreGraphics() error {
	cg, err := purego.Dlopen("/System/Library/Frameworks/CoreGraphics.framework/CoreGraphics", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return fmt.Errorf("input: cannot load CoreGraphics: %w", err)
	}
	cf, err := purego.Dlopen("/System/Library/Frameworks/CoreFoundation.framework/CoreFoundation", purego.RTLD_GLOBAL|purego.RTLD_NOW)
	if err != nil {
		return fmt.Errorf("input: cannot load CoreFoundation: %w", err)
	}
	cfLib = cf

	purego.RegisterLibFunc(&cgEventCreate, cg, "CGEventCreate")
	purego.RegisterLibFunc(&cgEventCreateMouseEvent, cg, "CGEventCreateMouseEvent")
	purego.RegisterLibFunc(&cgEventCreateScrollWheel, cg, "CGEventCreateScrollWheelEvent2")
	purego.RegisterLibFunc(&cgEventPost, cg, "CGEventPost")
	purego.RegisterLibFunc(&cgEventGetLocation, cg, "CGEventGetLocation")
	purego.RegisterLibFunc(&cgEventGetIntegerValueField, cg, "CGEventGetIntegerValueField")
	purego.RegisterLibFunc(&cgEventSetIntegerValueField, cg, "CGEventSetIntegerValueField")
	purego.RegisterLibFunc(&cgEventSourceCreate, cg, "CGEventSourceCreate")
	purego.RegisterLibFunc(&cgEventSourceSetUserData, cg, "CGEventSourceSetUserData")
	purego.RegisterLibFunc(&cgEventTapCreate, cg, "CGEventTapCreate")
	purego.RegisterLibFunc(&cgEventTapEnable, cg, "CGEventTapEnable")
	purego.RegisterLibFunc(&cgPreflightPostEventAccess, cg, "CGPreflightPostEventAccess")
	purego.RegisterLibFunc(&cgRequestPostEventAccess, cg, "CGRequestPostEventAccess")
	purego.RegisterLibFunc(&cgGetActiveDisplayList, cg, "CGGetActiveDisplayList")
	purego.RegisterLibFunc(&cgDisplayPixelsWide, cg, "CGDisplayPixelsWide")
	purego.RegisterLibFunc(&cgDisplayPixelsHigh, cg, "CGDisplayPixelsHigh")
	purego.RegisterLibFunc(&cgDisplayBounds, cg, "CGDisplayBounds")
	purego.RegisterLibFunc(&cgMainDisplayID, cg, "CGMainDisplayID")
	purego.RegisterLibFunc(&cgDisplayHideCursor, cg, "CGDisplayHideCursor")
	purego.RegisterLibFunc(&cgDisplayShowCursor, cg, "CGDisplayShowCursor")

	purego.RegisterLibFunc(&cfRelease, cf, "CFRelease")
	purego.RegisterLibFunc(&cfRunLoopGetCurrent, cf, "CFRunLoopGetCurrent")
	purego.RegisterLibFunc(&cfRunLoopAddSource, cf, "CFRunLoopAddSource")
	purego.RegisterLibFunc(&cfRunLoopRemoveSource, cf, "CFRunLoopRemoveSource")
	purego.RegisterLibFunc(&cfRunLoopRun, cf, "CFRunLoopRun")
	purego.RegisterLibFunc(&cfRunLoopStop, cf, "CFRunLoopStop")
	purego.RegisterLibFunc(&cfMachPortCreateRunLoopSource, cf, "CFMachPortCreateRunLoopSource")
	purego.RegisterLibFunc(&cfStringCreateWithCString, cf, "CFStringCreateWithCString")
	return nil
}

// darwinBackend captures the mouse of a Mac with an event tap of the window
// server, and moves it with the events the window server posts.
type darwinBackend struct {
	mu sync.Mutex

	// onEvent is the callback of the capture.
	onEvent func(Event)
	// events holds the events the tap gives, so that the callback of the
	// tap stays short and the window server never waits for it.
	events chan Event
	// done ends the dispatcher of the events.
	done chan struct{}
	// port is the tap of the capture.
	port uintptr
	// runSource is the source of the run loop of the tap.
	runSource uintptr
	// loop is the run loop of the thread of the tap.
	loop uintptr
	// mode is the mode of the run loop the source is added to.
	mode uintptr
	// started tells that the capture runs.
	started bool

	// eventSource is the source of the events the program posts. It comes
	// from the system of the input, so that the events look like the ones
	// of a mouse.
	eventSource uintptr

	// cursorX and cursorY are the place of the pointer on the desktop.
	cursorX float64
	cursorY float64
	// originX and originY are the place of the main screen on the desktop.
	originX float64
	originY float64
	// width and height are the size of the main screen.
	width  int
	height int
}

// init gives the program the backend of macOS.
func init() {
	Factory = newDarwinBackend
}

// newDarwinBackend builds the backend of macOS. It opens the frameworks of
// the window server and reads the screens of the computer.
func newDarwinBackend() (Backend, error) {
	if err := loadCoreGraphics(); err != nil {
		return nil, err
	}
	if !cgPreflightPostEventAccess() {
		// The settings give the permission, but the person has to put the
		// program in the list there. Asking the system for the access
		// shows its prompt, which does that; the grant itself is not
		// something the program can make.
		if cgRequestPostEventAccess != nil {
			cgRequestPostEventAccess()
		}
		if !cgPreflightPostEventAccess() {
			return nil, ErrAccessibility
		}
	}

	b := &darwinBackend{}
	b.readDisplays()

	source := cgEventSourceCreate(cgEventSourceStateHIDSystemState)
	if source != 0 {
		cgEventSourceSetUserData(source, cgUserDataMagic)
	}
	b.eventSource = source

	// The place of the pointer, so that the first movement of the mouse is
	// the movement of the pointer and not the walk from the corner of the
	// screen.
	if event := cgEventCreate(0); event != 0 {
		loc := cgEventGetLocation(event)
		b.cursorX, b.cursorY = loc.X, loc.Y
		cfRelease(event)
	}
	return b, nil
}

// readDisplays reads the screens of the computer and the place of the main
// screen, which is the one the desktop begins with.
func (b *darwinBackend) readDisplays() {
	var displays [16]uint32
	var count uint32
	if cgGetActiveDisplayList(uint32(len(displays)), &displays[0], &count) != 0 || count == 0 {
		return
	}
	main := cgMainDisplayID()
	for i := uint32(0); i < count; i++ {
		display := displays[i]
		width := int(cgDisplayPixelsWide(display))
		height := int(cgDisplayPixelsHigh(display))
		if width <= 0 || height <= 0 {
			continue
		}
		if display == main {
			bounds := cgDisplayBounds(display)
			b.originX, b.originY = bounds.X, bounds.Y
			b.width, b.height = width, height
			return
		}
	}
	// The main screen was not in the list, so the first one is used.
	display := displays[0]
	bounds := cgDisplayBounds(display)
	b.originX, b.originY = bounds.X, bounds.Y
	b.width = int(cgDisplayPixelsWide(display))
	b.height = int(cgDisplayPixelsHigh(display))
}

// Capture starts reading the mouse of the computer and calls onEvent for
// every event, until Stop is called.
func (b *darwinBackend) Capture(onEvent func(Event)) error {
	if onEvent == nil {
		return errors.New("input: the capture needs a callback")
	}
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return errors.New("input: the capture is already running")
	}
	b.onEvent = onEvent
	b.events = make(chan Event, 1024)
	b.done = make(chan struct{})
	b.started = true
	b.mu.Unlock()

	ready := make(chan error, 1)
	go b.run(ready)
	if err := <-ready; err != nil {
		b.mu.Lock()
		b.started = false
		b.mu.Unlock()
		return err
	}

	go b.dispatch(b.events, b.done, onEvent)
	return nil
}

// run makes the tap and runs the run loop of its thread.
//
// The window server calls the callback of the tap from the thread that runs
// the run loop, so the tap lives on a thread of its own.
func (b *darwinBackend) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	port := cgEventTapCreate(cgHIDEventTap, cgHeadInsertEventTap, cgEventTapOptionListenOnly, cgEventMask, darwinTapCallbackPtr, 0)
	if port == 0 {
		ready <- fmt.Errorf("%w: the event tap could not be made", ErrAccessibility)
		return
	}
	source := cfMachPortCreateRunLoopSource(0, port, 0)
	if source == 0 {
		cfRelease(port)
		ready <- errors.New("input: the run loop of the event tap could not be made")
		return
	}
	mode := darwinRunLoopMode()
	if mode == 0 {
		cfRelease(source)
		cfRelease(port)
		ready <- errors.New("input: the mode of the run loop could not be found")
		return
	}
	loop := cfRunLoopGetCurrent()

	b.mu.Lock()
	b.port = port
	b.runSource = source
	b.loop = loop
	b.mode = mode
	b.mu.Unlock()

	setDarwinBackend(b)
	defer setDarwinBackend(nil)

	cfRunLoopAddSource(loop, source, mode)
	cgEventTapEnable(port, true)
	ready <- nil

	cfRunLoopRun()

	cfRunLoopRemoveSource(loop, source, mode)
	b.mu.Lock()
	b.port = 0
	b.runSource = 0
	b.loop = 0
	b.mode = 0
	b.mu.Unlock()
	cfRelease(source)
	cfRelease(port)
}

// dispatch hands the events of the tap to the caller.
func (b *darwinBackend) dispatch(events chan Event, done <-chan struct{}, onEvent func(Event)) {
	for {
		select {
		case e := <-events:
			if onEvent != nil {
				onEvent(e)
			}
		case <-done:
			return
		}
	}
}

// Stop stops reading the mouse.
func (b *darwinBackend) Stop() error {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return nil
	}
	b.started = false
	loop := b.loop
	done := b.done
	b.mu.Unlock()

	if loop != 0 {
		cfRunLoopStop(loop)
	}
	if done != nil {
		close(done)
	}
	return nil
}

// emit puts an event in the queue of the capture.
func (b *darwinBackend) emit(e Event) {
	b.mu.Lock()
	events := b.events
	b.mu.Unlock()
	if events == nil {
		return
	}
	select {
	case events <- e:
	default:
		// The queue is full, which means that the caller is too slow.
		// The event is lost, so that the window server is never held
		// back by it.
	}
}

// emitMove reads a movement of the pointer. The window server gives the
// place of the pointer, so the movement is the difference with the place
// before.
func (b *darwinBackend) emitMove(event uintptr) {
	loc := cgEventGetLocation(event)

	b.mu.Lock()
	dx := int(loc.X - b.cursorX)
	dy := int(loc.Y - b.cursorY)
	b.cursorX, b.cursorY = loc.X, loc.Y
	originX, originY := b.originX, b.originY
	width, height := b.width, b.height
	b.mu.Unlock()

	x, y := int(loc.X-originX), int(loc.Y-originY)
	if width > 0 {
		if x < 0 {
			x = 0
		}
		if x > width-1 {
			x = width - 1
		}
	}
	if height > 0 {
		if y < 0 {
			y = 0
		}
		if y > height-1 {
			y = height - 1
		}
	}
	b.emit(Event{Kind: KindMove, DX: dx, DY: dy, X: x, Y: y})
}

// emitButton reads the press or the release of a button.
func (b *darwinBackend) emitButton(event uintptr, down bool) {
	var button Button
	switch cgEventGetIntegerValueField(event, cgFieldMouseButtonNumber) {
	case cgMouseButtonLeft:
		button = ButtonLeft
	case cgMouseButtonRight:
		button = ButtonRight
	case cgMouseButtonCenter:
		button = ButtonMiddle
	default:
		return
	}
	loc := cgEventGetLocation(event)
	b.mu.Lock()
	originX, originY := b.originX, b.originY
	b.mu.Unlock()
	b.emit(Event{
		Kind:   KindButton,
		Button: button,
		Down:   down,
		X:      int(loc.X - originX),
		Y:      int(loc.Y - originY),
	})
}

// emitWheel reads a turn of the wheel.
func (b *darwinBackend) emitWheel(event uintptr) {
	notches := darwinWheelNotches(event)
	if notches == 0 {
		return
	}
	loc := cgEventGetLocation(event)
	b.mu.Lock()
	originX, originY := b.originX, b.originY
	b.mu.Unlock()
	b.emit(Event{
		Kind:  KindWheel,
		Wheel: notches,
		X:     int(loc.X - originX),
		Y:     int(loc.Y - originY),
	})
}

// darwinWheelNotches turns the scrolling data of an event into a number of
// notches.
//
// The window server counts a turn of the wheel the other way round than the
// kernel of Linux and Windows do, unless it turned the direction of the
// device itself, which the field of the event says.
func darwinWheelNotches(event uintptr) int {
	var lines int64
	if cgEventGetIntegerValueField(event, cgFieldScrollIsContinuous) != 0 {
		// A trackpad scrolls by pixels, and the event says how many
		// lines that is in a number of sixteen bits.
		lines = cgEventGetIntegerValueField(event, cgFieldScrollFixedPtDeltaAxis1) >> 16
		if lines == 0 {
			pixels := cgEventGetIntegerValueField(event, cgFieldScrollPointDeltaAxis1)
			lines = pixels / 10
		}
	} else {
		// A wheel gives one line for every turn of it.
		lines = cgEventGetIntegerValueField(event, cgFieldScrollDeltaAxis1)
	}
	if cgEventGetIntegerValueField(event, cgFieldDirectionInvertedFromDevice) == 0 {
		lines = -lines
	}
	return int(lines)
}

// Move moves the pointer of the computer by a number of pixels.
func (b *darwinBackend) Move(dx, dy int) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	b.mu.Lock()
	x := b.cursorX + float64(dx)
	y := b.cursorY + float64(dy)
	b.mu.Unlock()
	return b.postMove(x, y)
}

// MoveTo puts the pointer of the computer at a place of the screen.
func (b *darwinBackend) MoveTo(x, y int) error {
	b.mu.Lock()
	gx := b.originX + float64(x)
	gy := b.originY + float64(y)
	b.mu.Unlock()
	return b.postMove(gx, gy)
}

// postMove puts the pointer at a place of the desktop and posts the event.
func (b *darwinBackend) postMove(x, y float64) error {
	b.mu.Lock()
	source := b.eventSource
	b.mu.Unlock()

	event := cgEventCreateMouseEvent(source, cgEventMouseMoved, cgPoint{X: x, Y: y}, cgMouseButtonLeft)
	if event == 0 {
		return errors.New("input: the event of the mouse could not be made")
	}
	defer cfRelease(event)

	cgEventSetIntegerValueField(event, cgFieldSourceUserData, cgUserDataMagic)
	cgEventPost(cgHIDEventTap, event)

	b.mu.Lock()
	b.cursorX, b.cursorY = x, y
	b.mu.Unlock()
	return nil
}

// Button presses or releases a button of the computer.
func (b *darwinBackend) Button(button Button, down bool) error {
	var typ uint32
	var number uint32
	switch button {
	case ButtonLeft:
		number = cgMouseButtonLeft
		if down {
			typ = cgEventLeftMouseDown
		} else {
			typ = cgEventLeftMouseUp
		}
	case ButtonRight:
		number = cgMouseButtonRight
		if down {
			typ = cgEventRightMouseDown
		} else {
			typ = cgEventRightMouseUp
		}
	case ButtonMiddle:
		number = cgMouseButtonCenter
		if down {
			typ = cgEventOtherMouseDown
		} else {
			typ = cgEventOtherMouseUp
		}
	default:
		return fmt.Errorf("input: unknown button %d", button)
	}

	b.mu.Lock()
	source := b.eventSource
	x, y := b.cursorX, b.cursorY
	b.mu.Unlock()

	event := cgEventCreateMouseEvent(source, typ, cgPoint{X: x, Y: y}, number)
	if event == 0 {
		return errors.New("input: the event of the mouse could not be made")
	}
	defer cfRelease(event)

	cgEventSetIntegerValueField(event, cgFieldSourceUserData, cgUserDataMagic)
	cgEventPost(cgHIDEventTap, event)
	return nil
}

// Wheel turns the wheel of the computer.
func (b *darwinBackend) Wheel(notches int) error {
	if notches == 0 {
		return nil
	}
	b.mu.Lock()
	source := b.eventSource
	b.mu.Unlock()

	// The wheel of the window server turns the other way round than the
	// one of the kernel of Linux and Windows, so the number of notches is
	// the other way round too.
	event := cgEventCreateScrollWheel(source, cgScrollEventUnitLine, 1, int32(-notches), 0, 0)
	if event == 0 {
		return errors.New("input: the event of the wheel could not be made")
	}
	defer cfRelease(event)

	cgEventSetIntegerValueField(event, cgFieldSourceUserData, cgUserDataMagic)
	cgEventPost(cgHIDEventTap, event)
	return nil
}

// Screens returns the screens of the computer.
//
// The main screen comes first, because the place of the pointer is given
// on it, and the other screens follow.
func (b *darwinBackend) Screens() []Screen {
	var displays [16]uint32
	var count uint32
	if cgGetActiveDisplayList(uint32(len(displays)), &displays[0], &count) != 0 || count == 0 {
		return nil
	}

	type placed struct {
		id     uint32
		screen Screen
	}
	list := make([]placed, 0, count)
	for i := uint32(0); i < count; i++ {
		display := displays[i]
		width := int(cgDisplayPixelsWide(display))
		height := int(cgDisplayPixelsHigh(display))
		if width <= 0 || height <= 0 {
			continue
		}
		bounds := cgDisplayBounds(display)
		list = append(list, placed{id: display, screen: Screen{
			X:      int(bounds.X),
			Y:      int(bounds.Y),
			Width:  width,
			Height: height,
		}})
	}

	// The main screen goes to the front of the list.
	main := cgMainDisplayID()
	for i, d := range list {
		if d.id == main {
			list[0], list[i] = list[i], list[0]
			break
		}
	}

	out := make([]Screen, 0, len(list))
	for _, d := range list {
		out = append(out, d.screen)
	}
	return out
}

// Primary returns the screen the pointer is on.
func (b *darwinBackend) Primary() Screen {
	screens := b.Screens()
	if len(screens) == 0 {
		return Screen{}
	}
	b.mu.Lock()
	x, y := b.cursorX, b.cursorY
	b.mu.Unlock()
	for _, s := range screens {
		if x >= float64(s.X) && x < float64(s.X+s.Width) &&
			y >= float64(s.Y) && y < float64(s.Y+s.Height) {
			return Screen{X: int(x), Y: int(y), Width: s.Width, Height: s.Height}
		}
	}
	s := screens[0]
	return Screen{X: int(x), Y: int(y), Width: s.Width, Height: s.Height}
}

// ShowCursor shows the cursor of the computer or hides it.
//
// Only the screen that holds the pointer of the sharing shows a cursor, so
// the cursor of the other screens is hidden while the pointer is elsewhere.
// The window server keeps the cursor of every screen on its own, so the
// cursor is taken away from all of them.
func (b *darwinBackend) ShowCursor(show bool) error {
	var displays [16]uint32
	var count uint32
	if cgGetActiveDisplayList(uint32(len(displays)), &displays[0], &count) != 0 || count == 0 {
		return errors.New("input: no screen to show the cursor on")
	}
	for i := uint32(0); i < count; i++ {
		display := displays[i]
		if show {
			cgDisplayShowCursor(display)
		} else {
			cgDisplayHideCursor(display)
		}
	}
	return nil
}

// darwinTapOwner is the backend of the tap of the mouse. There is one tap
// at a time, so the backend that runs is kept here.
var (
	darwinMu    sync.Mutex
	darwinOwner *darwinBackend
)

// darwinTapCallbackPtr is the callback of the tap as the window server
// wants it. It is kept, because the window server holds it as long as the
// tap lives.
var darwinTapCallbackPtr = purego.NewCallback(darwinTapCallback)

// setDarwinBackend keeps the backend that runs, so that the callback of the
// tap finds it.
func setDarwinBackend(b *darwinBackend) {
	darwinMu.Lock()
	darwinOwner = b
	darwinMu.Unlock()
}

// currentDarwinBackend returns the backend that runs.
func currentDarwinBackend() *darwinBackend {
	darwinMu.Lock()
	defer darwinMu.Unlock()
	return darwinOwner
}

// darwinTapCallback is the callback the window server calls for every event
// of the mouse.
func darwinTapCallback(proxy uintptr, typ uintptr, event uintptr, userInfo uintptr) uintptr {
	t := uint32(typ)
	if t == cgEventTapDisabledByTimeout || t == cgEventTapDisabledByUserInput {
		// The tap was turned off, so it is turned on again.
		b := currentDarwinBackend()
		if b != nil {
			b.mu.Lock()
			port := b.port
			b.mu.Unlock()
			if port != 0 {
				cgEventTapEnable(port, true)
			}
		}
		return event
	}
	if event == 0 {
		return event
	}
	// The events the program sends itself come back here, and must not
	// travel to the other devices again.
	if cgEventGetIntegerValueField(event, cgFieldSourceUserData) == cgUserDataMagic {
		return event
	}
	b := currentDarwinBackend()
	if b == nil {
		return event
	}
	switch t {
	case cgEventMouseMoved, cgEventLeftMouseDragged, cgEventRightMouseDragged, cgEventOtherMouseDragged:
		b.emitMove(event)
	case cgEventLeftMouseDown, cgEventRightMouseDown, cgEventOtherMouseDown:
		b.emitButton(event, true)
	case cgEventLeftMouseUp, cgEventRightMouseUp, cgEventOtherMouseUp:
		b.emitButton(event, false)
	case cgEventScrollWheel:
		b.emitWheel(event)
	}
	return event
}

// darwinRunLoopMode returns the mode of the run loop of the tap.
//
// The run loop of the thread of the tap runs the default mode, so the
// source of the tap is added to that mode. The name of the mode is made
// into a string of CoreFoundation, which the run loop knows as well as
// the constant of the framework.
func darwinRunLoopMode() uintptr {
	text := append([]byte("kCFRunLoopDefaultMode"), 0)
	mode := cfStringCreateWithCString(0, &text[0], cfStringEncodingUTF8)
	runtime.KeepAlive(text)
	return mode
}
