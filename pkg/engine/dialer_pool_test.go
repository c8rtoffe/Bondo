package engine

import (
	"context"
	"net"
	"testing"
	"time"

	"torrent-multipath/pkg/network"
	"torrent-multipath/pkg/state"
)

func TestDialerPoolRoundRobinSelection(t *testing.T) {
	// Start a local test TCP echo listener
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed starting test listener: %v", err)
	}
	defer listener.Close()

	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 64)
				n, _ := c.Read(buf)
				if n > 0 {
					_, _ = c.Write(buf[:n])
				}
			}(conn)
		}
	}()

	mockAdapters := []network.NetworkAdapter{
		{
			ID:            "eth0-127.0.0.1",
			Name:          "Adapter A",
			InterfaceName: "",
			IP:            net.ParseIP("127.0.0.1"),
			Type:          state.AdapterTypeEthernet,
			Color:         "#06B6D4",
			IsUp:          true,
		},
		{
			ID:            "wlan0-127.0.0.1",
			Name:          "Adapter B",
			InterfaceName: "",
			IP:            net.ParseIP("127.0.0.1"),
			Type:          state.AdapterTypeWiFi,
			Color:         "#10B981",
			IsUp:          true,
		},
	}

	monitor := network.NewMonitor()
	defer monitor.Stop()

	pool := NewDialerPool(mockAdapters, monitor)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Dial 4 times to verify cycling
	for i := 0; i < 4; i++ {
		conn, err := pool.Dial(ctx, listener.Addr().String())
		if err != nil {
			t.Fatalf("Dial #%d failed: %v", i, err)
		}
		// Write test bytes
		_, err = conn.Write([]byte("ping"))
		if err != nil {
			t.Fatalf("Write on conn #%d failed: %v", i, err)
		}
		buf := make([]byte, 4)
		_, err = conn.Read(buf)
		if err != nil {
			t.Fatalf("Read on conn #%d failed: %v", i, err)
		}
		conn.Close()
	}

	stats := pool.GetAdapterStats()
	if len(stats) != 2 {
		t.Fatalf("expected 2 adapter stats, got %d", len(stats))
	}

	totalDials := stats[0].TotalConnections + stats[1].TotalConnections
	if totalDials != 4 {
		t.Errorf("expected 4 total dials across adapters, got %d", totalDials)
	}

	// Sockets should be closed now
	if stats[0].ActiveConnections != 0 || stats[1].ActiveConnections != 0 {
		t.Errorf("expected 0 active connections after close, got A=%d, B=%d",
			stats[0].ActiveConnections, stats[1].ActiveConnections)
	}
}

func TestDialerPoolToggleAdapter(t *testing.T) {
	mockAdapters := []network.NetworkAdapter{
		{
			ID:   "nic1",
			Name: "NIC 1",
			IP:   net.ParseIP("192.168.1.10"),
			IsUp: true,
		},
		{
			ID:   "nic2",
			Name: "NIC 2",
			IP:   net.ParseIP("192.168.2.20"),
			IsUp: true,
		},
	}

	pool := NewDialerPool(mockAdapters, nil)
	pool.ToggleAdapter("nic1", false)

	stats := pool.GetAdapterStats()
	for _, s := range stats {
		if s.ID == "nic1" && (s.Enabled || s.Active) {
			t.Errorf("nic1 should be disabled")
		}
		if s.ID == "nic2" && (!s.Enabled || !s.Active) {
			t.Errorf("nic2 should be enabled and active")
		}
	}
}
