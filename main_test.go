package main

import (
	"testing"

	"github.com/egoist/mygo/ui"
)

// The view runs without a window in tests, which click and type as a user
// would.
func TestView(t *testing.T) {
	a := &app{
		name:  "Test",
		id:    "test",
		role:  roleHost,
		edge:  "right",
		stop:  make(chan struct{}),
		status: "",
	}
	tt := ui.NewTester(a.view, 540, 600)

	if !tt.HasText("MmouseShare") {
		t.Errorf("the window does not show the name of the program, texts %q", tt.Texts())
	}
	if !tt.HasText("启动共享") {
		t.Errorf("the window does not offer to start the sharing, texts %q", tt.Texts())
	}
	if !tt.HasText("主机（共享鼠标）") {
		t.Errorf("the window does not show the role of the device, texts %q", tt.Texts())
	}
}

// TestViewOfTheClient shows the settings of a client instead of the ones
// of the host.
func TestViewOfTheClient(t *testing.T) {
	a := &app{
		name: "Test",
		id:   "test",
		role: roleClient,
		edge: "right",
		stop: make(chan struct{}),
	}
	tt := ui.NewTester(a.view, 540, 600)

	if !tt.HasText("连接主机") {
		t.Errorf("the window does not offer to join a host, texts %q", tt.Texts())
	}
	if !tt.HasText("主机地址") {
		t.Errorf("the window does not ask for the address of the host, texts %q", tt.Texts())
	}
}

// TestRoleNameAndSetRole makes sure that the role of the device follows
// what the interface shows.
func TestRoleNameAndSetRole(t *testing.T) {
	a := &app{role: roleHost, stop: make(chan struct{})}
	if a.roleName() != "主机（共享鼠标）" {
		t.Errorf("the role is %q, want the host", a.roleName())
	}
	a.setRole("客户端（跟随主机）")
	if a.role != roleClient {
		t.Errorf("the role is %d, want the client", a.role)
	}
	if a.roleName() != "客户端（跟随主机）" {
		t.Errorf("the role is %q, want the client", a.roleName())
	}
}
