package engine

import (
	"context"
	"fmt"
	"net"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"torrent-multipath/pkg/network"
	"torrent-multipath/pkg/state"
)

// MonitoredConn accounts for actual socket bytes and closes exactly once.
type MonitoredConn struct {
	net.Conn
	adapterID string
	monitor   *network.Monitor
	route     *PeerRouteInfo
	onClose   func()
	closeOnce sync.Once
	closeErr  error
}

func (c *MonitoredConn) Read(b []byte) (n int, err error) {
	n, err = c.Conn.Read(b)
	if n > 0 {
		if c.monitor != nil {
			c.monitor.RecordRead(c.adapterID, int64(n))
		}
		if c.route != nil {
			c.route.BytesDownloaded.Add(int64(n))
		}
	}
	return
}

func (c *MonitoredConn) Write(b []byte) (n int, err error) {
	n, err = c.Conn.Write(b)
	if n > 0 {
		if c.monitor != nil {
			c.monitor.RecordWrite(c.adapterID, int64(n))
		}
		if c.route != nil {
			c.route.BytesUploaded.Add(int64(n))
		}
	}
	return
}

func (c *MonitoredConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.Conn.Close()
		if c.onClose != nil {
			c.onClose()
		}
	})
	return c.closeErr
}

type PeerRouteInfo struct {
	ID              string
	PeerIP          string
	AdapterID       string
	AdapterName     string
	AdapterIP       string
	ConnectedAt     int64
	BytesDownloaded atomic.Int64
	BytesUploaded   atomic.Int64
}

// DialerPool routes outgoing peer TCP sockets only through selected adapters.
type DialerPool struct {
	mu          sync.RWMutex
	adapters    []network.NetworkAdapter
	enabled     map[string]bool
	activeConns map[string]*atomic.Int32
	totalConns  map[string]*atomic.Int64
	counter     atomic.Uint64
	monitor     *network.Monitor
	connections map[*MonitoredConn]struct{}
	closed      bool
}

func NewDialerPool(adapters []network.NetworkAdapter, monitor *network.Monitor) *DialerPool {
	p := &DialerPool{
		enabled: make(map[string]bool), activeConns: make(map[string]*atomic.Int32),
		totalConns: make(map[string]*atomic.Int64), connections: make(map[*MonitoredConn]struct{}), monitor: monitor,
	}
	p.SetAdapters(adapters)
	return p
}

func (p *DialerPool) DialerNetwork() string { return "tcp4" }

func (p *DialerPool) Dial(ctx context.Context, addr string) (net.Conn, error) {
	p.mu.RLock()
	candidates := make([]network.NetworkAdapter, 0, len(p.adapters))
	for _, a := range p.adapters {
		if a.IsUp && p.enabled[a.ID] {
			candidates = append(candidates, a)
		}
	}
	closed := p.closed
	p.mu.RUnlock()
	if closed {
		return nil, net.ErrClosed
	}
	if len(candidates) == 0 {
		return nil, fmt.Errorf("no enabled network adapters; select a network before downloading")
	}
	start := int((p.counter.Add(1) - 1) % uint64(len(candidates)))
	var lastErr error
	for i := 0; i < len(candidates); i++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		a := candidates[(start+i)%len(candidates)]
		d := network.CreateBoundDialer(a.IP, a.InterfaceName, network.DefaultSocketConfig())
		conn, err := d.DialContext(ctx, "tcp4", addr)
		if err != nil {
			lastErr = err
			continue
		}
		p.mu.Lock()
		available := false
		for _, current := range p.adapters {
			if current.ID == a.ID && current.IsUp && p.enabled[a.ID] {
				available = true
				break
			}
		}
		if p.closed || !available || ctx.Err() != nil {
			p.mu.Unlock()
			conn.Close()
			lastErr = fmt.Errorf("adapter %s is no longer selected", a.Name)
			continue
		}
		active := p.activeConns[a.ID]
		active.Add(1)
		p.totalConns[a.ID].Add(1)
		c := &MonitoredConn{Conn: conn, adapterID: a.ID, monitor: p.monitor, route: &PeerRouteInfo{
			ID: conn.LocalAddr().String() + "->" + addr, PeerIP: addr, AdapterID: a.ID, AdapterName: a.Name,
			AdapterIP: conn.LocalAddr().(*net.TCPAddr).IP.String(), ConnectedAt: time.Now().Unix(),
		}}
		c.onClose = func() {
			p.mu.Lock()
			delete(p.connections, c)
			active.Add(-1)
			p.mu.Unlock()
		}
		p.connections[c] = struct{}{}
		p.mu.Unlock()
		return c, nil
	}
	return nil, fmt.Errorf("peer connection to %s failed across %d adapters: %w", addr, len(candidates), lastErr)
}

func (p *DialerPool) SetAdapters(adapters []network.NetworkAdapter) {
	p.mu.Lock()
	p.adapters = append([]network.NetworkAdapter(nil), adapters...)
	present := make(map[string]bool)
	for _, a := range adapters {
		present[a.ID] = a.IsUp
		if _, ok := p.enabled[a.ID]; !ok {
			p.enabled[a.ID] = true
		}
		if p.activeConns[a.ID] == nil {
			p.activeConns[a.ID] = &atomic.Int32{}
		}
		if p.totalConns[a.ID] == nil {
			p.totalConns[a.ID] = &atomic.Int64{}
		}
		if p.monitor != nil {
			p.monitor.RegisterAdapterWithIP(a.ID, a.IP)
		}
	}
	var closeList []*MonitoredConn
	for c := range p.connections {
		if !present[c.adapterID] {
			closeList = append(closeList, c)
		}
	}
	p.mu.Unlock()
	for _, c := range closeList {
		c.Close()
	}
}

func (p *DialerPool) ToggleAdapter(id string, enabled bool) {
	p.mu.Lock()
	if _, exists := p.enabled[id]; !exists {
		p.mu.Unlock()
		return
	}
	p.enabled[id] = enabled
	var closeList []*MonitoredConn
	if !enabled {
		for c := range p.connections {
			if c.adapterID == id {
				closeList = append(closeList, c)
			}
		}
	}
	p.mu.Unlock()
	for _, c := range closeList {
		c.Close()
	}
}

func (p *DialerPool) GetAdapterStats() []state.AdapterStats {
	p.mu.RLock()
	defer p.mu.RUnlock()
	stats := make([]state.AdapterStats, 0, len(p.adapters))
	for _, a := range p.adapters {
		var dl, ul, totalDl, totalUl, ping int64
		history := []int64{}
		if p.monitor != nil {
			dl, ul, totalDl, totalUl, history, ping = p.monitor.GetMetrics(a.ID)
		}
		stats = append(stats, state.AdapterStats{
			ID: a.ID, Name: a.Name, InterfaceName: a.InterfaceName, IP: a.IP.String(), HardwareAddr: a.HardwareAddr,
			Type: a.Type, Active: a.IsUp && p.enabled[a.ID], Enabled: p.enabled[a.ID], DownloadSpeed: dl, UploadSpeed: ul,
			TotalDownloaded: totalDl, TotalUploaded: totalUl, ActiveConnections: int(p.activeConns[a.ID].Load()),
			TotalConnections: p.totalConns[a.ID].Load(), Color: a.Color, SpeedHistory: history, PingMs: ping,
		})
	}
	return stats
}

func (p *DialerPool) GetPeerDiagnostics() []state.PeerDiagnostic {
	p.mu.RLock()
	defer p.mu.RUnlock()
	diagnostics := make([]state.PeerDiagnostic, 0, len(p.connections))
	for c := range p.connections {
		r := c.route
		diagnostics = append(diagnostics, state.PeerDiagnostic{
			ID: r.ID, PeerIP: r.PeerIP, AdapterID: r.AdapterID, AdapterName: r.AdapterName, AdapterIP: r.AdapterIP,
			ConnectedAt: r.ConnectedAt, BytesDownloaded: r.BytesDownloaded.Load(), BytesUploaded: r.BytesUploaded.Load(), Client: "BitTorrent Peer",
		})
	}
	sort.Slice(diagnostics, func(i, j int) bool {
		if diagnostics[i].PeerIP == diagnostics[j].PeerIP {
			if diagnostics[i].AdapterID == diagnostics[j].AdapterID {
				return diagnostics[i].ID < diagnostics[j].ID
			}
			return diagnostics[i].AdapterID < diagnostics[j].AdapterID
		}
		return diagnostics[i].PeerIP < diagnostics[j].PeerIP
	})
	return diagnostics
}

func (p *DialerPool) Close() {
	p.mu.Lock()
	p.closed = true
	list := make([]*MonitoredConn, 0, len(p.connections))
	for c := range p.connections {
		list = append(list, c)
	}
	p.mu.Unlock()
	for _, c := range list {
		c.Close()
	}
}
