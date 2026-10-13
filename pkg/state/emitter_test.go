package state

import (
	"encoding/json"
	"strings"
	"testing"
)

type snapshotProvider struct{ err string }

func (p snapshotProvider) GetError() string { return p.err }

func (snapshotProvider) GetAdapters() []AdapterStats {
	return []AdapterStats{{Active: false, TotalDownloaded: 100, DownloadSpeed: 4}}
}
func (snapshotProvider) GetTorrents() []TorrentInfo                     { return nil }
func (snapshotProvider) GetPeerDiagnostics() []PeerDiagnostic           { return nil }
func (snapshotProvider) CalculateBondEfficiency([]AdapterStats) float64 { return 0 }

func TestSnapshotsKeepTotalsAndEncodeEmptyLists(t *testing.T) {
	stats := BuildSnapshot(snapshotProvider{})
	if stats.TotalDownloaded != 100 || stats.ActiveAdapterCount != 0 {
		t.Fatal("disabled adapter lost its totals")
	}
	for _, s := range []DownloadStats{stats, BuildSnapshot(nil)} {
		data, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), ":null") {
			t.Fatalf("snapshot contains null arrays: %s", data)
		}
	}
}

func TestLiveSnapshotsKeepAndClearExternalLinkErrors(t *testing.T) {
	provider := snapshotProvider{err: "Cannot open magnet link: invalid infohash"}
	for i := 0; i < 3; i++ {
		if got := BuildSnapshot(provider).Error; got != provider.err {
			t.Fatalf("live update lost the error: %q", got)
		}
	}
	provider.err = ""
	if got := BuildSnapshot(provider).Error; got != "" {
		t.Fatalf("resolved error remains in telemetry: %q", got)
	}
}
