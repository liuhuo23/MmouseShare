// MmouseShare shares one mouse between the computers of a local network.
//
// The program runs on every computer. One of them is the host: it holds
// the mouse and the others follow it. A pointer that walks off the edge of
// a screen shows up on the screen next to it, so the pointer crosses from
// one computer to the other without a jump.
//
// The interface is drawn by MyGo itself, in Go, with no web page.
package main

import (
	"bytes"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"log"
	"os"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"

	"mmouseshare/internal/discovery"
	"mmouseshare/internal/input"
	"mmouseshare/internal/protocol"
	"mmouseshare/internal/session"
)

// defaultHostPort is the port the host listens on.
const defaultHostPort = 27301

// accessibilitySettingsURL is the part of the settings of macOS where the
// person allows the program to read and to post the input of the computer.
const accessibilitySettingsURL = "x-apple.systempreferences:com.apple.preference.security?Privacy_Accessibility"

// The role of the device.
const (
	roleHost = iota
	roleClient
)

// edges are the sides of the virtual desktop a device can sit on.
var edges = []string{"right", "left", "top", "bottom"}

// app is the state the window shows.
type app struct {
	// name is the name of this device.
	name string
	// id identifies this device.
	id string

	// session does the sharing.
	session *session.Session

	// role tells whether the device is the host or a client.
	role int
	// running tells that the sharing is on.
	running bool
	// status is the last thing that happened.
	status string
	// waitsAccessibility tells that the last failure was the permission of
	// the accessibility, which the window offers to open the settings for.
	waitsAccessibility bool
	// address is the address of the host a client joins.
	address string
	// edge is where the device sits in the virtual desktop.
	edge string

	// devices are the devices of the virtual desktop.
	devices []protocol.DeviceInfo
	// found are the hosts that were found on the network.
	found []discovery.Device
	// pointerX and pointerY are where the pointer is, in the virtual
	// desktop.
	pointerX int
	pointerY int
	// active is the screen the pointer is on.
	active string
	// controller is the device whose mouse holds the pointer.
	controller string

	// window is the window of the program.
	window *mygo.Window
	// tray is the icon of the program in the tray.
	tray *mygo.Tray
	// stop ends the keep alive.
	stop chan struct{}
}

// main starts the program.
func main() {
	a := &app{
		name: deviceName(),
		id:   fmt.Sprintf("%s-%d", hostName(), time.Now().UnixNano()%100000),
		role: roleHost,
		edge: "right",
		stop: make(chan struct{}),
	}

	mygo.App.WhenReady(func() {
		a.session = session.New(a.name, a.id, session.ModeHost)
		a.session.OnChange(a.refresh)

		a.show()
		a.startTray()
	})

	// The window can be closed without the program leaving: the icon in
	// the tray brings it back.
	mygo.App.OnWindowAllClosed(func() {})

	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}

// show opens the window of the program.
func (a *app) show() {
	if a.window == nil || a.window.IsDestroyed() {
		a.window = mygo.NewWindow(mygo.WindowOptions{
			Title:     "MmouseShare",
			Width:     540,
			Height:    600,
			MinWidth:  440,
			MinHeight: 460,
			StateKey:  "main",
			Content:   ui.View(a.view),
		})
	}
	a.window.Show()
	a.window.Focus()
}

// view builds the interface from the state.
func (a *app) view(c *ui.Context) {
	t := c.Theme()
	ui.Column(c).Fill().Padding(18).Gap(14).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "MmouseShare").FontSize(22).Bold()
			ui.Spacer(c)
			a.roleBadge(c, t)
		})
		ui.Text(c, "把一只鼠标共享到局域网的多台电脑，指针走到屏幕边缘即可切屏。").
			FontSize(13).TextColor(t.TextMuted)

		ui.Divider(c)

		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "角色").Width(64).FontSize(13)
			role := a.roleName()
			ui.Select(c, &role, []string{"主机（共享鼠标）", "客户端（跟随主机）"}).
				Width(220).OnChange(func() {
				a.setRole(role)
			})
		})

		if a.role == roleHost {
			a.hostView(c, t)
		} else {
			a.clientView(c, t)
		}

		ui.Divider(c)

		ui.Text(c, "虚拟桌面上的设备").FontSize(14).Bold()
		a.deviceList(c, t)

		if a.status != "" {
			ui.Text(c, a.status).FontSize(12).TextColor(t.TextMuted)
		}
		if a.waitsAccessibility {
			ui.Text(c, "在「系统设置 → 隐私与安全性 → 辅助功能」里勾选 MmouseShare，然后退出并重新打开本程序，再点一次。").
				FontSize(12).TextColor(t.TextMuted)
			ui.Text(c, "系统只在程序启动时读取这项授权，勾选后不重新打开仍会提示失败。").
				FontSize(12).TextColor(t.TextMuted)
			if ui.Button(c, "打开辅助功能设置").Width(160).Clicked() {
				c.OpenURL(accessibilitySettingsURL)
			}
		}
	})
}

// roleBadge tells whether the sharing is on.
func (a *app) roleBadge(c *ui.Context, t *ui.Theme) {
	if a.running {
		ui.Badge(c, "共享中").TextColor(t.Success)
		return
	}
	ui.Badge(c, "未启动").TextColor(t.TextMuted)
}

// hostView shows the settings of the host.
func (a *app) hostView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "监听端口").Width(64).FontSize(13)
			ui.Text(c, fmt.Sprint(defaultHostPort)).Width(64).FontSize(13)
		})
		ui.Row(c).Gap(10).Children(func() {
			if a.running {
				if ui.Button(c, "停止共享").Width(120).Clicked() {
					a.stopHost()
				}
			} else if ui.PrimaryButton(c, "启动共享").Width(120).Clicked() {
				a.startHost()
			}
		})
	})
}

// clientView shows the settings of a client.
func (a *app) clientView(c *ui.Context, t *ui.Theme) {
	ui.Column(c).Gap(10).Children(func() {
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "主机地址").Width(64).FontSize(13)
			ui.TextInput(c, &a.address).Width(180).Placeholder("192.168.1.10:27301")
		})
		ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Text(c, "所在边缘").Width(64).FontSize(13)
			edge := a.edge
			ui.Select(c, &edge, edges).Width(140).OnChange(func() {
				a.setEdge(edge)
			})
		})
		ui.Row(c).Gap(10).Children(func() {
			if a.running {
				if ui.Button(c, "断开").Width(120).Clicked() {
					a.stopClient()
				}
			} else if ui.PrimaryButton(c, "连接主机").Width(120).Clicked() {
				a.startClient()
			}
			if !a.running && ui.Button(c, "扫描局域网设备").Width(140).Clicked() {
				a.scan()
			}
		})

		// The hosts that were found on the network.
		if len(a.found) > 0 {
			ui.Text(c, "在局域网中找到的主机").FontSize(13).Bold()
			for _, d := range a.found {
				d := d
				ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
					ui.Text(c, fmt.Sprintf("%s  %s", d.Name, d.Address)).FontSize(12)
					if ui.Link(c, "连接", "").Clicked() {
						a.address = d.Address
						a.startClient()
					}
				})
			}
		}
	})
}

// deviceList shows the devices of the virtual desktop.
func (a *app) deviceList(c *ui.Context, t *ui.Theme) {
	if len(a.devices) == 0 {
		ui.Text(c, "还没有设备。启动共享后，同一局域网的其他设备会出现在这里。").
			FontSize(12).TextColor(t.TextMuted)
		return
	}
	for _, d := range a.devices {
		d := d
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			name := d.Name
			if name == "" {
				name = d.ID
			}
			if d.Self {
				name += "（本机）"
			}
			ui.Text(c, name).Width(140).FontSize(13)
			ui.Text(c, fmt.Sprintf("%d×%d  %s", d.Width, d.Height, d.Edge)).
				Width(160).FontSize(12).TextColor(t.TextMuted)
			if d.Active {
				ui.Badge(c, "指针在此").TextColor(t.Success)
			} else if d.Online {
				ui.Badge(c, "在线").TextColor(t.TextMuted)
			} else {
				ui.Badge(c, "离线").TextColor(t.TextMuted)
			}
		})
	}
}

// roleName returns the name of the role of the device, as the interface
// shows it.
func (a *app) roleName() string {
	if a.role == roleHost {
		return "主机（共享鼠标）"
	}
	return "客户端（跟随主机）"
}

// setRole changes what the device does.
func (a *app) setRole(role string) {
	next := roleClient
	if role == "主机（共享鼠标）" {
		next = roleHost
	}
	if next == a.role {
		return
	}
	a.role = next
	a.stopHost()
	a.stopClient()
	a.refresh()
}

// setEdge changes where the device sits in the virtual desktop.
func (a *app) setEdge(edge string) {
	a.edge = edge
	if a.session != nil {
		_ = a.session.SetEdge(protocol.Edge(edge))
	}
}

// fail puts the error of the starting of the sharing in the window. The one
// of the permission of the accessibility is the only case the person can fix
// in the settings, so it says where to go and the window offers to open it.
func (a *app) fail(prefix string, err error) {
	a.waitsAccessibility = errors.Is(err, input.ErrAccessibility)
	if a.waitsAccessibility {
		a.status = prefix + "macOS 还没允许 MmouseShare 使用「辅助功能」。"
		return
	}
	a.status = prefix + err.Error()
}

// startHost starts the sharing of the mouse of this device.
func (a *app) startHost() {
	if a.session == nil {
		return
	}
	if err := a.session.StartHost(defaultHostPort); err != nil {
		a.fail("启动失败：", err)
		a.refresh()
		return
	}
	a.running = true
	a.waitsAccessibility = false
	a.status = "正在共享鼠标，其他设备可以连接了。"
	go a.session.KeepAlive(a.stop)
	a.refresh()
}

// stopHost stops the sharing of the mouse of this device.
func (a *app) stopHost() {
	if a.session == nil {
		return
	}
	a.session.Stop()
	a.running = false
	a.status = "共享已停止。"
	a.refresh()
}

// startClient joins the host of the network.
func (a *app) startClient() {
	if a.session == nil {
		return
	}
	addr := strings.TrimSpace(a.address)
	if addr == "" {
		a.status = "请先填写主机地址。"
		a.refresh()
		return
	}
	if !strings.Contains(addr, ":") {
		addr = fmt.Sprintf("%s:%d", addr, defaultHostPort)
	}
	if err := a.session.StartClient(addr); err != nil {
		a.fail("连接失败：", err)
		a.refresh()
		return
	}
	a.running = true
	a.waitsAccessibility = false
	a.status = "已连接到 " + addr
	go a.session.KeepAlive(a.stop)
	a.refresh()
}

// stopClient leaves the host of the network.
func (a *app) stopClient() {
	if a.session == nil {
		return
	}
	a.session.Stop()
	a.running = false
	a.status = "已断开。"
	a.refresh()
}

// scan looks for the hosts of the local network.
func (a *app) scan() {
	if a.session == nil {
		return
	}
	if err := a.session.Scan(); err != nil {
		a.status = "扫描失败：" + err.Error()
		a.refresh()
		return
	}
	a.status = "正在查找局域网中的主机……"
	go func() {
		time.Sleep(2 * time.Second)
		a.refresh()
	}()
	a.refresh()
}

// refresh takes the state of the session into the window.
func (a *app) refresh() {
	if a.session == nil {
		return
	}
	a.devices = a.session.Devices()
	a.found = a.session.Found()
	a.active = a.session.Active()
	a.controller = a.session.Controller()
	p := a.session.Pointer()
	a.pointerX = p.X
	a.pointerY = p.Y

	if a.window != nil && !a.window.IsDestroyed() {
		a.window.Invalidate()
	}
}

// startTray puts the icon of the program in the tray.
func (a *app) startTray() {
	tray, err := mygo.NewTray(mygo.TrayOptions{
		ID:             "mmouseshare",
		Icon:           trayIcon(),
		IconIsTemplate: true,
		ToolTip:        "MmouseShare",
		Menu: mygo.NewMenu([]*mygo.MenuItem{
			{Label: "打开窗口", Click: func(*mygo.MenuItem, *mygo.Window) { a.show() }},
			{Label: "开始 / 停止共享", Click: func(*mygo.MenuItem, *mygo.Window) {
				if a.running {
					a.stopHost()
					a.stopClient()
				} else if a.role == roleHost {
					a.startHost()
				} else {
					a.startClient()
				}
			}},
			mygo.Separator(),
			{Role: mygo.RoleQuit},
		}),
	})
	if err != nil {
		log.Println("tray:", err)
		return
	}
	a.tray = tray
}

// trayIcon draws a 32x32 template image: a mouse.
func trayIcon() []byte {
	img := image.NewNRGBA(image.Rect(0, 0, 32, 32))
	// The body of the mouse.
	for y := 4; y < 28; y++ {
		for x := 9; x < 23; x++ {
			// The top of the mouse is round.
			if y < 9 {
				dx := x - 16
				dy := y - 9
				if dx*dx+dy*dy > 49 {
					continue
				}
			}
			img.Set(x, y, color.Black)
		}
	}
	// The line between the buttons.
	for y := 4; y < 14; y++ {
		img.Set(15, y, color.Transparent)
		img.Set(16, y, color.Transparent)
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

// deviceName returns the name of this device, as the user knows it.
func deviceName() string {
	name, err := os.Hostname()
	if err != nil || name == "" {
		name = "MmouseShare"
	}
	return name
}

// hostName returns the short name of this device.
func hostName() string {
	name, err := os.Hostname()
	if err != nil {
		return "device"
	}
	name = strings.Split(name, ".")[0]
	if name == "" {
		return "device"
	}
	return name
}
