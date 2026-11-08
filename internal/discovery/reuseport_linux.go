//go:build linux

package discovery

// soReusePort is SO_REUSEPORT, which the beacons share their port with. It is
// the same number on every linux, and the syscall package does not name it.
const soReusePort = 0x0F
