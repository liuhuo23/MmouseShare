//go:build darwin

package discovery

import "syscall"

// soReusePort is SO_REUSEPORT, which the beacons share their port with.
const soReusePort = syscall.SO_REUSEPORT
