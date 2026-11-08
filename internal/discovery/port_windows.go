package discovery

import "syscall"

// reusePort tells the system that the port of the beacons is shared. Windows
// has no SO_REUSEPORT; its SO_REUSEADDR lets a port be bound twice, which is
// what the beacons need.
func reusePort(network, address string, c syscall.RawConn) error {
	var setErr error
	if err := c.Control(func(fd uintptr) {
		setErr = syscall.SetsockoptInt(syscall.Handle(fd), syscall.SOL_SOCKET, syscall.SO_REUSEADDR, 1)
	}); err != nil {
		return err
	}
	return setErr
}
