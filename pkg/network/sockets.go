package network

import (
	"net"
	"time"
)

// SocketConfig holds socket configuration options for dialers.
type SocketConfig struct {
	Timeout   time.Duration
	KeepAlive time.Duration
}

// DefaultSocketConfig provides reliable peer connection defaults.
func DefaultSocketConfig() SocketConfig {
	return SocketConfig{
		Timeout:   12 * time.Second,
		KeepAlive: 30 * time.Second,
	}
}

// CreateBoundDialer returns a net.Dialer whose local address is bound to the given adapter IP.
// This bypasses standard single-default-gateway routing by instructing the OS network stack
// to send packets from the physical adapter owning this local IP.
func CreateBoundDialer(localIP net.IP, ifaceName string, cfg SocketConfig) *net.Dialer {
	d := &net.Dialer{
		LocalAddr: &net.TCPAddr{
			IP:   localIP,
			Port: 0, // OS assigns an ephemeral port on this interface
		},
		Timeout:   cfg.Timeout,
		KeepAlive: cfg.KeepAlive,
	}

	// Attach OS-specific socket control hooks where applicable
	attachSocketControl(d, ifaceName)

	return d
}
