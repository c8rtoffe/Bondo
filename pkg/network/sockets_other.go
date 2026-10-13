//go:build !linux

package network

import (
	"net"
)

// Windows and other platforms use standard LocalAddr binding.
func attachSocketControl(d *net.Dialer, ifaceName string) {
	// Standard LocalAddr binding
}
