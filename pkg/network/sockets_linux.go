//go:build linux

package network

import (
	"net"
	"syscall"
)

// attachSocketControl on Linux binds socket to the network interface name via SO_BINDTODEVICE.
func attachSocketControl(d *net.Dialer, ifaceName string) {
	if ifaceName == "" {
		return
	}
	d.Control = func(network, address string, c syscall.RawConn) error {
		var sockErr error
		err := c.Control(func(fd uintptr) {
			// SO_BINDTODEVICE = 25
			sockErr = syscall.SetsockoptString(int(fd), syscall.SOL_SOCKET, 25, ifaceName)
		})
		if err != nil {
			return err
		}
		return sockErr
	}
}
