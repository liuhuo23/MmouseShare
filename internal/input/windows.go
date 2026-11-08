//go:build windows

package input

import (
	"fmt"
	"runtime"
	"sync"
	"syscall"
	"unsafe"
)

// The Windows user32.dll and kernel32.dll calls, bound by hand so that the
// program stays free of cgo.
var (
	user32   = syscall.NewLazyDLL("user32.dll")
	kernel32 = syscall.NewLazyDLL("kernel32.dll")

	procSetWindowsHookEx    = user32.NewProc("SetWindowsHookExW")
	procUnhookWindowsHookEx = user32.NewProc("UnhookWindowsHookEx")
	procCallNextHookEx      = user32.NewProc("CallNextHookEx")
	procGetMessage          = user32.NewProc("GetMessageW")
	procTranslateMessage    = user32.NewProc("TranslateMessage")
	procDispatchMessage     = user32.NewProc("DispatchMessageW")
	procPostThreadMessage   = user32.NewProc("PostThreadMessageW")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procSendInput           = user32.NewProc("SendInput")
	procGetCursorPos        = user32.NewProc("GetCursorPos")
	procGetCurrentThreadId  = kernel32.NewProc("GetCurrentThreadId")
	procGetModuleHandle     = kernel32.NewProc("GetModuleHandleW")
)

// The Windows messages the mouse hook reports.
const (
	wmMouseMove   = 0x0200
	wmLButtonDown = 0x0201
	wmLButtonUp   = 0x0202
	wmRButtonDown = 0x0204
	wmRButtonUp   = 0x0205
	wmMButtonDown = 0x0207
	wmMButtonUp   = 0x0208
	wmMouseWheel  = 0x020A
	wmMouseHWheel = 0x020E
	wmQuit        = 0x0012
)

// The system metrics of interest.
const (
	smCXScreen        = 0
	smCYScreen        = 1
	smXVirtualScreen  = 76
	smYVirtualScreen  = 77
	smCXVirtualScreen = 78
	smCYVirtualScreen = 79
)

// The flags of the input events.
const (
	inputMouse           = 0
	mouseEventFMove      = 0x0001
	mouseEventFLeftDown  = 0x0002
	mouseEventFLeftUp    = 0x0004
	mouseEventFRightDown = 0x0008
	mouseEventFRightUp   = 0x0010
	mouseEventFMiddleDown = 0x0020
	mouseEventFMiddleUp   = 0x0040
	mouseEventFAbsolute    = 0x8000
	mouseEventFWheel       = 0x0800
)

// The hook of the low level mouse.
const (
	whMouseLL = 14
	hcAction  = 0
)

// llMhfInjected tells that an event was made by SendInput, so that the
// events the program sends itself do not come back to it.
const llMhfInjected = 0x00000001

// point is a POINT of Win32.
type point struct {
	X int32
	Y int32
}

// mouseHookData is a MSLLHOOKSTRUCT of Win32.
type mouseHookData struct {
	Pt        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// inputStruct is an INPUT of Win32.
type inputStruct struct {
	Type uint32
	_    uint32
	MI   mouseInput
}

// mouseInput is a MOUSEINPUT of Win32.
type mouseInput struct {
	DX        int32
	DY        int32
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

// winMsg is a MSG of Win32.
type winMsg struct {
	Hwnd    uintptr
	Message uint32
	WParam  uintptr
	LParam  uintptr
	Time    uint32
	_       uint32
}

// WinBackend captures the mouse of a Windows computer with a low level
// hook, and moves it with SendInput.
type WinBackend struct {
	mu      sync.Mutex
	hook    syscall.Handle
	thread  uint32
	onEvent func(Event)
	lastX   int
	lastY   int
	started bool
	done    chan struct{}
	closed  chan struct{}
}

func init() {
	Factory = newWinBackend
}

func newWinBackend() (Backend, error) {
	return &WinBackend{}, nil
}

// Capture starts the mouse hook.
//
// Windows asks that the hook lives on a thread of its own that runs a
// message loop, so the hook is installed on a thread locked for that.
func (b *WinBackend) Capture(onEvent func(Event)) error {
	b.mu.Lock()
	if b.started {
		b.mu.Unlock()
		return fmt.Errorf("input: the capture is already running")
	}
	b.onEvent = onEvent
	b.done = make(chan struct{})
	b.closed = make(chan struct{})
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
	return nil
}

// run installs the hook and runs the message loop of its thread.
func (b *WinBackend) run(ready chan<- error) {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()

	thread, _, _ := procGetCurrentThreadId.Call()
	b.mu.Lock()
	b.thread = uint32(thread)
	b.mu.Unlock()

	// The place of the pointer before the hook is made, so that the first
	// movement of the mouse is the movement of the pointer and not the
	// walk from the corner of the screen.
	var p point
	if ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); ret != 0 {
		b.mu.Lock()
		b.lastX, b.lastY = int(p.X), int(p.Y)
		b.mu.Unlock()
	}

	module, _, _ := procGetModuleHandle.Call(0)
	callback := syscall.NewCallback(b.hookProc)
	hook, _, err := procSetWindowsHookEx.Call(
		uintptr(whMouseLL),
		callback,
		module,
		0,
	)
	if hook == 0 {
		ready <- fmt.Errorf("input: SetWindowsHookEx failed: %w", err)
		return
	}
	b.mu.Lock()
	b.hook = syscall.Handle(hook)
	b.mu.Unlock()
	ready <- nil

	var msg winMsg
	for {
		ret, _, _ := procGetMessage.Call(uintptr(unsafe.Pointer(&msg)), 0, 0, 0)
		if int32(ret) <= 0 {
			break
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&msg)))
		procDispatchMessage.Call(uintptr(unsafe.Pointer(&msg)))
	}

	b.mu.Lock()
	if b.hook != 0 {
		procUnhookWindowsHookEx.Call(uintptr(b.hook))
		b.hook = 0
	}
	b.mu.Unlock()
	close(b.closed)
}

// Stop ends the mouse hook.
func (b *WinBackend) Stop() error {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return nil
	}
	b.started = false
	thread := b.thread
	b.mu.Unlock()

	if thread != 0 {
		procPostThreadMessage.Call(uintptr(thread), wmQuit, 0, 0)
	}
	return nil
}

// hookProc is the callback Windows calls for every mouse event.
func (b *WinBackend) hookProc(code int32, wParam uintptr, lParam uintptr) uintptr {
	if code == hcAction {
		data := (*mouseHookData)(unsafe.Pointer(lParam))
		// The events the program sends itself come back here, and must
		// not travel to the other devices again.
		if data.Flags&llMhfInjected == 0 {
			switch msg := uint32(wParam); msg {
			case wmMouseMove:
				b.emit(b.moveEvent(data))
			case wmLButtonDown:
				b.emit(b.buttonEvent(data, ButtonLeft, true))
			case wmLButtonUp:
				b.emit(b.buttonEvent(data, ButtonLeft, false))
			case wmRButtonDown:
				b.emit(b.buttonEvent(data, ButtonRight, true))
			case wmRButtonUp:
				b.emit(b.buttonEvent(data, ButtonRight, false))
			case wmMButtonDown:
				b.emit(b.buttonEvent(data, ButtonMiddle, true))
			case wmMButtonUp:
				b.emit(b.buttonEvent(data, ButtonMiddle, false))
			case wmMouseWheel, wmMouseHWheel:
				b.emit(b.wheelEvent(data))
			}
		}
	}
	ret, _, _ := procCallNextHookEx.Call(0, uintptr(code), wParam, lParam)
	return ret
}

// emit hands an event to the caller.
func (b *WinBackend) emit(e Event) {
	b.mu.Lock()
	onEvent := b.onEvent
	b.mu.Unlock()
	if onEvent != nil {
		onEvent(e)
	}
}

// moveEvent reads a movement of the pointer. Windows gives the place of
// the pointer, so the movement is the difference with the place before.
func (b *WinBackend) moveEvent(data *mouseHookData) Event {
	b.mu.Lock()
	dx := int(data.Pt.X) - b.lastX
	dy := int(data.Pt.Y) - b.lastY
	b.lastX = int(data.Pt.X)
	b.lastY = int(data.Pt.Y)
	b.mu.Unlock()
	return Event{Kind: KindMove, DX: dx, DY: dy, X: int(data.Pt.X), Y: int(data.Pt.Y)}
}

// buttonEvent reads the press or the release of a button.
func (b *WinBackend) buttonEvent(data *mouseHookData, btn Button, down bool) Event {
	return Event{Kind: KindButton, Button: btn, Down: down, X: int(data.Pt.X), Y: int(data.Pt.Y)}
}

// wheelEvent reads a turn of the wheel.
func (b *WinBackend) wheelEvent(data *mouseHookData) Event {
	return Event{Kind: KindWheel, Wheel: int(int16(data.MouseData >> 16)), X: int(data.Pt.X), Y: int(data.Pt.Y)}
}

// Move moves the pointer by a number of pixels.
func (b *WinBackend) Move(dx, dy int) error {
	if dx == 0 && dy == 0 {
		return nil
	}
	return b.sendInput(int32(dx), int32(dy), 0, mouseEventFMove)
}

// MoveTo puts the pointer at a place of the screen.
func (b *WinBackend) MoveTo(x, y int) error {
	left, _, _ := procGetSystemMetrics.Call(uintptr(smXVirtualScreen))
	top, _, _ := procGetSystemMetrics.Call(uintptr(smYVirtualScreen))
	width, _, _ := procGetSystemMetrics.Call(uintptr(smCXVirtualScreen))
	height, _, _ := procGetSystemMetrics.Call(uintptr(smCYVirtualScreen))
	if width <= 0 || height <= 0 {
		return fmt.Errorf("input: no screen to move the pointer on")
	}
	// SendInput wants the place on the whole desktop, from 0 to 65535.
	nx := (int64(x-int(left)) * 65535) / int64(width)
	ny := (int64(y-int(top)) * 65535) / int64(height)
	if nx < 0 {
		nx = 0
	}
	if ny < 0 {
		ny = 0
	}
	if nx > 65535 {
		nx = 65535
	}
	if ny > 65535 {
		ny = 65535
	}
	return b.sendInput(int32(nx), int32(ny), 0, mouseEventFAbsolute|mouseEventFMove)
}

// Button presses or releases a button.
func (b *WinBackend) Button(btn Button, down bool) error {
	var flags uint32
	switch btn {
	case ButtonLeft:
		if down {
			flags = mouseEventFLeftDown
		} else {
			flags = mouseEventFLeftUp
		}
	case ButtonRight:
		if down {
			flags = mouseEventFRightDown
		} else {
			flags = mouseEventFRightUp
		}
	case ButtonMiddle:
		if down {
			flags = mouseEventFMiddleDown
		} else {
			flags = mouseEventFMiddleUp
		}
	default:
		return fmt.Errorf("input: unknown button %d", btn)
	}
	return b.sendInput(0, 0, 0, flags)
}

// Wheel turns the wheel.
func (b *WinBackend) Wheel(notches int) error {
	if notches == 0 {
		return nil
	}
	return b.sendInput(0, 0, uint32(int32(notches)*120), mouseEventFWheel)
}

// sendInput sends one mouse event to Windows.
func (b *WinBackend) sendInput(dx, dy int32, data uint32, flags uint32) error {
	in := inputStruct{
		Type: inputMouse,
		MI: mouseInput{
			DX:        dx,
			DY:        dy,
			MouseData: data,
			Flags:     flags,
		},
	}
	sent, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in))
	if sent == 0 {
		return fmt.Errorf("input: SendInput failed: %w", err)
	}
	return nil
}

// Screens returns the screens of the computer. Windows reports the whole
// desktop as one screen, which is what the pointer moves on.
func (b *WinBackend) Screens() []Screen {
	width, _, _ := procGetSystemMetrics.Call(uintptr(smCXVirtualScreen))
	height, _, _ := procGetSystemMetrics.Call(uintptr(smCYVirtualScreen))
	left, _, _ := procGetSystemMetrics.Call(uintptr(smXVirtualScreen))
	top, _, _ := procGetSystemMetrics.Call(uintptr(smYVirtualScreen))
	if width == 0 || height == 0 {
		return nil
	}
	return []Screen{{X: int(left), Y: int(top), Width: int(width), Height: int(height)}}
}

// Primary returns the screen the pointer is on.
func (b *WinBackend) Primary() Screen {
	var p point
	if ret, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p))); ret == 0 {
		return Screen{}
	}
	screens := b.Screens()
	if len(screens) == 0 {
		return Screen{}
	}
	s := screens[0]
	// The place of the pointer is given on the whole desktop, so the
	// place of the screen is taken off it.
	return Screen{X: int(p.X) - s.X, Y: int(p.Y) - s.Y, Width: s.Width, Height: s.Height}
}
