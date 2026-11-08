//go:build darwin || linux

package discovery

import "syscall"

// reusePort tells the system that the port of the beacons is shared, so that
// the broadcast of a hub reaches every scanner of the machine, and so that
// two programs that listen to it can run at once.
func reusePort(network, address string, c syscall.RawConn) error {
	var setErr error
	if err := c.Control(func(fd uintptr) {
		if err := syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1); err != nil {
			setErr = err
			return
		}
		setErr = syscall.SetsockoptInt(int(fd), syscall.SOL_SOCKET, soReusePort, 1)
	}); err != nil {
		return err
	}
	return setErr
}
