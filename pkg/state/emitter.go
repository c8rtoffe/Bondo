package state

import (
	"context"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"sync"
	"time"
)

type DataProvider interface {
	GetError() string
	GetAdapters() []AdapterStats
	GetTorrents() []TorrentInfo
	GetPeerDiagnostics() []PeerDiagnostic
	CalculateBondEfficiency([]AdapterStats) float64
}

type Emitter struct {
	mu       sync.Mutex
	provider DataProvider
	cancel   context.CancelFunc
	done     chan struct{}
}

func NewEmitter(provider DataProvider) *Emitter { return &Emitter{provider: provider} }

func (e *Emitter) Start(ctx context.Context) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	e.cancel = cancel
	done := make(chan struct{})
	e.done = done
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				runtime.EventsEmit(ctx, "stats:update", BuildSnapshot(e.provider))
			}
		}
	}()
}

func BuildSnapshot(p DataProvider) DownloadStats {
	stats := DownloadStats{Adapters: []AdapterStats{}, Torrents: []TorrentInfo{}, PeerDiagnostics: []PeerDiagnostic{}}
	if p == nil {
		return stats
	}
	stats.Error = p.GetError()
	if a := p.GetAdapters(); a != nil {
		stats.Adapters = a
	}
	if t := p.GetTorrents(); t != nil {
		stats.Torrents = t
	}
	if d := p.GetPeerDiagnostics(); d != nil {
		stats.PeerDiagnostics = d
	}
	for i := range stats.Adapters {
		if stats.Adapters[i].SpeedHistory == nil {
			stats.Adapters[i].SpeedHistory = []int64{}
		}
	}
	for i := range stats.Torrents {
		if stats.Torrents[i].Files == nil {
			stats.Torrents[i].Files = []TorrentFile{}
		}
	}
	for _, a := range stats.Adapters {
		// Existing bytes must stay in totals when an adapter is toggled off.
		stats.TotalDownloadSpeed += a.DownloadSpeed
		stats.TotalUploadSpeed += a.UploadSpeed
		stats.TotalDownloaded += a.TotalDownloaded
		stats.TotalUploaded += a.TotalUploaded
		if a.Active {
			stats.ActiveAdapterCount++
		}
	}
	stats.BondEfficiency = p.CalculateBondEfficiency(stats.Adapters)
	return stats
}

func (e *Emitter) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.cancel == nil {
		return
	}
	e.cancel()
	<-e.done
	e.cancel = nil
}
