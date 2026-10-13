package network

import (
	"context"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"torrent-multipath/pkg/state"
)

const (
	maxHistoryPoints  = 30
	defaultSampleRate = 500 * time.Millisecond
)

type adapterMetrics struct {
	downloadBytesDelta atomic.Int64
	uploadBytesDelta   atomic.Int64
	totalDownloaded    atomic.Int64
	totalUploaded      atomic.Int64
	currentDlSpeed     int64
	currentUlSpeed     int64
	speedHistory       []int64
	pingMs             atomic.Int64
	localIP            net.IP
}

// Monitor tracks real-time read and write bandwidth per network adapter.
type Monitor struct {
	mu           sync.RWMutex
	adapters     map[string]*adapterMetrics
	ticker       *time.Ticker
	pingTicker   *time.Ticker
	stopChan     chan struct{}
	lastSampleAt time.Time
	stopOnce     sync.Once
	done         chan struct{}
	ctx          context.Context
	cancel       context.CancelFunc
	probes       sync.WaitGroup
}

// NewMonitor initializes a new per-adapter bandwidth monitor.
func NewMonitor() *Monitor {
	ctx, cancel := context.WithCancel(context.Background())
	m := &Monitor{ctx: ctx, cancel: cancel, done: make(chan struct{}),
		adapters:     make(map[string]*adapterMetrics),
		stopChan:     make(chan struct{}),
		lastSampleAt: time.Now(),
	}
	m.ticker = time.NewTicker(defaultSampleRate)
	m.pingTicker = time.NewTicker(2500 * time.Millisecond)
	go m.sampleLoop()
	return m
}

// RegisterAdapterWithIP adds an adapter with its local IP for latency tracking.
func (m *Monitor) RegisterAdapterWithIP(adapterID string, ip net.IP) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if metrics, exists := m.adapters[adapterID]; exists {
		if ip != nil {
			metrics.localIP = ip
		}
	} else {
		m.adapters[adapterID] = &adapterMetrics{
			speedHistory: make([]int64, 0, maxHistoryPoints),
			localIP:      ip,
		}
	}
}

// RecordRead accumulates inbound bytes for the specified adapter.
func (m *Monitor) RecordRead(adapterID string, n int64) {
	if n <= 0 {
		return
	}
	m.mu.RLock()
	metrics, ok := m.adapters[adapterID]
	m.mu.RUnlock()

	if ok {
		metrics.downloadBytesDelta.Add(n)
		metrics.totalDownloaded.Add(n)
	}
}

// RecordWrite accumulates outbound bytes for the specified adapter.
func (m *Monitor) RecordWrite(adapterID string, n int64) {
	if n <= 0 {
		return
	}
	m.mu.RLock()
	metrics, ok := m.adapters[adapterID]
	m.mu.RUnlock()

	if ok {
		metrics.uploadBytesDelta.Add(n)
		metrics.totalUploaded.Add(n)
	}
}

// sampleLoop ticks every 500ms to calculate instant speed (bytes/sec) and record history.
func (m *Monitor) sampleLoop() {
	defer close(m.done)
	// Trigger initial ping probe
	m.startProbe()

	for {
		select {
		case <-m.ticker.C:
			m.sample()
		case <-m.pingTicker.C:
			m.startProbe()
		case <-m.stopChan:
			return
		}
	}
}

func (m *Monitor) startProbe() {
	m.probes.Add(1)
	go func() { defer m.probes.Done(); m.probePings() }()
}

func (m *Monitor) probePings() {
	m.mu.RLock()
	type probeTarget struct {
		metrics *adapterMetrics
		ip      net.IP
	}
	var targets []probeTarget
	for _, metrics := range m.adapters {
		if metrics.localIP != nil {
			targets = append(targets, probeTarget{metrics, metrics.localIP})
		}
	}
	m.mu.RUnlock()

	for _, t := range targets {
		m.probes.Add(1)
		go func(met *adapterMetrics, localIP net.IP) {
			defer m.probes.Done()
			start := time.Now()
			ctx, cancel := context.WithTimeout(m.ctx, 2*time.Second)
			defer cancel()

			dialer := net.Dialer{
				LocalAddr: &net.TCPAddr{IP: localIP, Port: 0},
				Timeout:   800 * time.Millisecond,
			}
			conn, err := dialer.DialContext(ctx, "tcp", "1.1.1.1:53")
			if err != nil {
				start = time.Now()
				conn, err = dialer.DialContext(ctx, "tcp", "8.8.8.8:53")
			}
			if err == nil {
				rtt := time.Since(start).Milliseconds()
				conn.Close()
				met.pingMs.Store(max(rtt, 1))
			} else {
				met.pingMs.Store(0)
			}
		}(t.metrics, t.ip)
	}
}

func (m *Monitor) sample() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(m.lastSampleAt).Seconds()
	if elapsed <= 0 {
		elapsed = 0.5
	}
	m.lastSampleAt = now

	for _, metrics := range m.adapters {
		dlBytes := metrics.downloadBytesDelta.Swap(0)
		ulBytes := metrics.uploadBytesDelta.Swap(0)

		metrics.currentDlSpeed = int64(float64(dlBytes) / elapsed)
		metrics.currentUlSpeed = int64(float64(ulBytes) / elapsed)

		// Append to history slice
		if len(metrics.speedHistory) >= maxHistoryPoints {
			metrics.speedHistory = append(metrics.speedHistory[1:], metrics.currentDlSpeed)
		} else {
			metrics.speedHistory = append(metrics.speedHistory, metrics.currentDlSpeed)
		}
	}
}

// GetMetrics returns snapshot stats for a specific adapter.
func (m *Monitor) GetMetrics(adapterID string) (dlSpeed, ulSpeed, totalDl, totalUl int64, history []int64, pingMs int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	metrics, ok := m.adapters[adapterID]
	if !ok {
		return 0, 0, 0, 0, nil, 0
	}

	histCopy := make([]int64, len(metrics.speedHistory))
	copy(histCopy, metrics.speedHistory)

	return metrics.currentDlSpeed,
		metrics.currentUlSpeed,
		metrics.totalDownloaded.Load(),
		metrics.totalUploaded.Load(),
		histCopy,
		metrics.pingMs.Load()
}

// Normalized Jain fairness: 100 for evenly shared traffic, 0 for one of many adapters.
func CalculateBondEfficiency(adapters []state.AdapterStats) float64 {
	var sum, squares float64
	count := 0
	for _, a := range adapters {
		if a.Active && a.Enabled {
			count++
			speed := float64(max(a.DownloadSpeed, 0))
			sum += speed
			squares += speed * speed
		}
	}
	if squares == 0 || count == 0 {
		return 0
	}
	if count == 1 {
		return 50
	}
	n := float64(count)
	fairness := sum * sum / (n * squares)
	return max(0, min(100, (fairness-1/n)/(1-1/n)*100))
}

// Stop cleanly terminates the background monitor ticker.
func (m *Monitor) Stop() {
	m.stopOnce.Do(func() {
		m.cancel()
		m.ticker.Stop()
		m.pingTicker.Stop()
		close(m.stopChan)
		<-m.done
		m.probes.Wait()
	})
}
