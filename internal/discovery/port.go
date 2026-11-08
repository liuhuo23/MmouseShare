package discovery

import (
	"context"
	"fmt"
	"net"
)

// listenBeacons listens to the port the beacons travel on, on the socket that
// shares it with the other programs of the machine: a hub and the device that
// looks for it are often the same program, and the two devices of a room are
// sometimes tried on one computer before they are put on two.
func listenBeacons() (net.PacketConn, error) {
	lc := net.ListenConfig{Control: reusePort}
	return lc.ListenPacket(context.Background(), "udp4", fmt.Sprintf(":%d", DefaultPort))
}
