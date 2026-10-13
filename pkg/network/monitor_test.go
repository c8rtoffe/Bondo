package network

import (
	"math"
	"sync"
	"testing"
	"time"
	"torrent-multipath/pkg/state"
)

func TestMonitorCountersAndIdempotentStop(t *testing.T) {
	m := NewMonitor()
	m.RegisterAdapterWithIP("nic", nil)
	m.RecordRead("nic", 100)
	m.RecordWrite("nic", 30)
	m.mu.Lock()
	m.lastSampleAt = time.Now().Add(-time.Second)
	m.mu.Unlock()
	m.sample()
	dl, ul, totalDl, totalUl, history, _ := m.GetMetrics("nic")
	if dl <= 0 || ul <= 0 || totalDl != 100 || totalUl != 30 || len(history) != 1 {
		t.Fatal("invalid monitor snapshot")
	}
	history[0] = 999
	_, _, _, _, again, _ := m.GetMetrics("nic")
	if again[0] == 999 {
		t.Fatal("snapshot exposed mutable history")
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Go(m.Stop)
	}
	wg.Wait()
}

func TestBondBalanceHandlesZeroTrafficAndOverflow(t *testing.T) {
	adapter := func(speed int64) state.AdapterStats {
		return state.AdapterStats{Active: true, Enabled: true, DownloadSpeed: speed}
	}
	tests := []struct {
		adapters []state.AdapterStats
		want     float64
	}{
		{nil, 0}, {[]state.AdapterStats{adapter(10)}, 50},
		{[]state.AdapterStats{adapter(10), adapter(10)}, 100},
		{[]state.AdapterStats{adapter(10), adapter(0)}, 0},
		{[]state.AdapterStats{adapter(math.MaxInt64), adapter(math.MaxInt64)}, 100},
	}
	for _, test := range tests {
		got := CalculateBondEfficiency(test.adapters)
		if math.Abs(got-test.want) > 0.00001 {
			t.Fatalf("got %f, want %f", got, test.want)
		}
	}
}
