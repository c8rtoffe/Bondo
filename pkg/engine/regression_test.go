package engine

import (
	"bytes"
	"context"
	"crypto/sha1"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"torrent-multipath/pkg/network"
	"torrent-multipath/pkg/state"
)

func testInfo(t *testing.T) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	hash := sha1.Sum([]byte("test"))
	raw, err := bencode.Marshal(metainfo.Info{Name: "payload.bin", Length: 4, PieceLength: 4, Pieces: hash[:]})
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: raw}
	var buf bytes.Buffer
	if err := mi.Write(&buf); err != nil {
		t.Fatal(err)
	}
	return mi, buf.Bytes()
}

func TestSourceValidationAndHTTP(t *testing.T) {
	for _, input := range []string{strings.Repeat("1", 40), strings.Repeat("B", 32), "magnet:?dn=name&xt=urn:btih:" + strings.Repeat("1", 40)} {
		if _, err := loadTorrentSpec(context.Background(), input, http.DefaultClient); err != nil {
			t.Fatalf("%s: %v", input, err)
		}
	}
	for _, input := range []string{"", "invalid", "magnet:?dn=missing", "magnet:?xt=urn:btih:" + strings.Repeat("0", 40), "file:///unknown", strings.Repeat("0", 32), "magnet:?xt=urn:btih:" + strings.Repeat("1", 40) + "&dn=%ZZ"} {
		if _, err := loadTorrentSpec(context.Background(), input, http.DefaultClient); err == nil {
			t.Fatalf("accepted invalid source %q", input)
		}
	}
	mi, data := testInfo(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/good":
			w.Write(data)
		case "/large":
			w.Write(bytes.Repeat([]byte("x"), maxMetainfoBytes+1))
		case "/bad":
			w.Write([]byte("<html>not a torrent</html>"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()
	spec, err := loadTorrentSpec(context.Background(), server.URL+"/good", server.Client())
	if err != nil || spec.InfoHash != mi.HashInfoBytes() {
		t.Fatalf("HTTP metainfo: %v", err)
	}
	for _, route := range []string{"/bad", "/missing", "/large"} {
		if _, err := loadTorrentSpec(context.Background(), server.URL+route, server.Client()); err == nil {
			t.Fatalf("accepted %s", route)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := loadTorrentSpec(ctx, server.URL+"/good", server.Client()); err == nil {
		t.Fatal("ignored cancelled request")
	}
}

func TestLocalTorrentFileValidation(t *testing.T) {
	mi, data := testInfo(t)
	root := t.TempDir()
	path := filepath.Join(root, "valid torrent.TORRENT")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	spec, err := loadTorrentFileSpec(path)
	if err != nil || spec.InfoHash != mi.HashInfoBytes() {
		t.Fatalf("local metainfo: %v", err)
	}
	for _, item := range []struct {
		name string
		data []byte
	}{
		{"wrong.txt", data},
		{"invalid.torrent", []byte("not torrent metainfo")},
		{"empty.torrent", nil},
		{"large.torrent", bytes.Repeat([]byte("x"), maxMetainfoBytes+1)},
	} {
		path := filepath.Join(root, item.name)
		if err := os.WriteFile(path, item.data, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadTorrentFileSpec(path); err == nil {
			t.Fatalf("accepted %s", item.name)
		}
	}
	directory := filepath.Join(root, "directory.torrent")
	if err := os.Mkdir(directory, 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{directory, filepath.Join(root, "missing.torrent"), ""} {
		if _, err := loadTorrentFileSpec(path); err == nil {
			t.Fatalf("accepted invalid path %q", path)
		}
	}
}

func TestTorrentFileDuplicateKeepsPausedTransfer(t *testing.T) {
	e := testEngine(t)
	_, data := testInfo(t)
	path := filepath.Join(t.TempDir(), "source.torrent")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	id, err := e.AddTorrentFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	original := e.torrents[id]
	for _, add := range []func() (string, error){
		func() (string, error) { return e.AddTorrentFile(path) },
		func() (string, error) { return e.AddMagnet(id) },
	} {
		duplicate, err := add()
		if err != nil || duplicate != id {
			t.Fatalf("duplicate: %s %v", duplicate, err)
		}
		e.mu.RLock()
		preserved := len(e.torrents) == 1 && e.torrents[id] == original && original.paused
		e.mu.RUnlock()
		if !preserved {
			t.Fatal("duplicate replaced or resumed a paused transfer")
		}
	}
	e.Close()
	if _, err := e.AddTorrentFile(path); err == nil {
		t.Fatal("closed engine accepted a torrent file")
	}
}

func testEngine(t *testing.T) *Engine {
	t.Helper()
	root, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	cfg := engineClientConfig(root.GetBaseDir())
	cfg.NoDHT = true

	client, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	monitor := network.NewMonitor()
	pool := NewDialerPool(nil, monitor)
	client.AddDialer(pool)
	ctx, cancel := context.WithCancel(context.Background())
	e := &Engine{client: client, storage: root, monitor: monitor, dialerPool: pool, torrents: make(map[string]*torrentEntry), ctx: ctx, cancel: cancel}
	t.Cleanup(e.Close)
	return e
}

func TestPauseBeforeMetadataDuplicateAndDirectoryChanges(t *testing.T) {
	e := testEngine(t)
	mi, _ := testInfo(t)
	id, err := e.AddMagnet(mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatal(err)
	}
	if err = e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	e.mu.RLock()
	original := e.torrents[id]
	e.mu.RUnlock()
	if duplicate, err := e.AddMagnet("magnet:?xt=urn:btih:" + id); err != nil || duplicate != id {
		t.Fatalf("duplicate: %s %v", duplicate, err)
	}
	if e.torrents[id] != original || !original.paused {
		t.Fatal("duplicate reset paused transfer")
	}
	if err = original.t.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	// GetTorrents and the metadata worker both use the engine lock; poll real priorities.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if original.t.Piece(0).State().Priority != torrent.PiecePriorityNone {
			t.Fatal("metadata arrival resumed a paused torrent")
		}
		time.Sleep(5 * time.Millisecond)
	}
	newRoot := t.TempDir()
	if err = e.storage.SetBaseDir(newRoot); err != nil {
		t.Fatal(err)
	}
	if e.GetTorrents()[0].DownloadDir != original.downloadDir {
		t.Fatal("folder change rewrote an existing transfer path")
	}
	id2, err := e.AddMagnet(strings.Repeat("2", 40))
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Dir(e.torrents[id2].downloadDir) != newRoot {
		t.Fatal("new transfer did not use the new root")
	}
	if err = e.ResumeTorrent(id); err != nil {
		t.Fatal(err)
	}
	if original.t.Piece(0).State().Priority == torrent.PiecePriorityNone {
		t.Fatal("resume did not request pieces")
	}
	oldFile := filepath.Join(original.downloadDir, "keep.bin")
	if err = os.WriteFile(oldFile, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	newFile := filepath.Join(newRoot, "keep.bin")
	if err = os.WriteFile(newFile, []byte("new"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = e.RemoveTorrent(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(original.downloadDir); !os.IsNotExist(err) {
		t.Fatalf("old transfer directory was not deleted: %v", err)
	}
	if _, err = os.Stat(newFile); err != nil {
		t.Fatal("deletion affected the newly selected root")
	}
	e.Close()
	e.Close()
	if _, err = e.AddMagnet(strings.Repeat("3", 40)); err == nil {
		t.Fatal("closed engine accepted a torrent")
	}
}

func TestRemoveWhileWaitingForMetadata(t *testing.T) {
	e := testEngine(t)
	id, err := e.AddMagnet(strings.Repeat("4", 40))
	if err != nil {
		t.Fatal(err)
	}
	target := e.torrents[id].t
	if err = e.RemoveTorrent(id, false); err != nil {
		t.Fatal(err)
	}
	select {
	case <-target.Closed():
	case <-time.After(time.Second):
		t.Fatal("removed torrent stayed open")
	}
	if len(e.GetTorrents()) != 0 {
		t.Fatal("removed transfer still listed")
	}
}

func TestStorageRejectsUnsafeTargets(t *testing.T) {
	s, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"", ".", "..", "../outside", "/outside"} {
		if err = s.RemoveTorrentFiles(s.GetBaseDir(), id); err == nil {
			t.Fatalf("accepted deletion target %q", id)
		}
	}
	if err = s.SetBaseDir(""); err == nil {
		t.Fatal("accepted empty storage root")
	}
	// Symlink creation may require Windows developer mode, so test where available.
	id := strings.Repeat("5", 40)
	outside := t.TempDir()
	if err = os.Symlink(outside, filepath.Join(s.GetBaseDir(), id)); err == nil {
		if _, _, err = s.CreateTorrentDir(id); err == nil {
			t.Fatal("accepted a symlink transfer directory")
		}
		if err = s.RemoveTorrentFiles(s.GetBaseDir(), id); err == nil {
			t.Fatal("accepted symlink deletion")
		}
	}
}

func echoListener(t *testing.T) net.Listener {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			c, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	return listener
}

func loopAdapters() []network.NetworkAdapter {
	return []network.NetworkAdapter{
		{ID: "a", Name: "A", IP: net.ParseIP("127.0.0.1"), IsUp: true},
		{ID: "b", Name: "B", IP: net.ParseIP("127.0.0.1"), IsUp: true},
	}
}

func TestPoolCountsDuplicateEndpointsAndDisabling(t *testing.T) {
	listener := echoListener(t)
	p := NewDialerPool(loopAdapters(), nil)
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	c1, err := p.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c2, err := p.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if len(p.GetPeerDiagnostics()) != 2 {
		t.Fatal("same remote endpoint overwrote a route")
	}
	if _, err = c1.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err = io.ReadFull(c1, buf); err != nil {
		t.Fatal(err)
	}
	routes := p.GetPeerDiagnostics()
	var dl, ul int64
	for _, r := range routes {
		dl += r.BytesDownloaded
		ul += r.BytesUploaded
	}
	if dl != 4 || ul != 4 {
		t.Fatalf("wrong peer byte counters: %d/%d", dl, ul)
	}
	c1.Close()
	c1.Close()
	if len(p.GetPeerDiagnostics()) != 1 {
		t.Fatal("closing one socket removed another route")
	}
	p.ToggleAdapter("b", false)
	c2.SetReadDeadline(time.Now().Add(time.Second))
	if _, err = c2.Read(buf); err == nil {
		t.Fatal("disabled adapter kept its socket open")
	}
	p.ToggleAdapter("a", false)
	if _, err = p.Dial(ctx, listener.Addr().String()); err == nil {
		t.Fatal("all disabled adapters fell back to default routing")
	}
	if len(p.GetPeerDiagnostics()) != 0 {
		t.Fatal("closed sockets still listed")
	}
}

func TestConcurrentDialRescanAndFallback(t *testing.T) {
	listener := echoListener(t)
	p := NewDialerPool(loopAdapters(), nil)
	t.Cleanup(p.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Go(func() {
			c, err := p.Dial(ctx, listener.Addr().String())
			if err != nil {
				t.Errorf("dial: %v", err)
				return
			}
			c.Close()
		})
		wg.Go(func() { p.SetAdapters(loopAdapters()); p.GetAdapterStats(); p.GetPeerDiagnostics() })
	}
	wg.Wait()
	for _, a := range p.GetAdapterStats() {
		if a.ActiveConnections != 0 {
			t.Fatal("connection counter leaked")
		}
	}
	bad := network.NetworkAdapter{ID: "unbound", Name: "Unbound", IP: net.ParseIP("192.0.2.1"), IsUp: true}
	p.SetAdapters([]network.NetworkAdapter{bad, loopAdapters()[0]})
	p.counter.Store(0)
	c, err := p.Dial(ctx, listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	if p.GetPeerDiagnostics()[0].AdapterID != "a" {
		t.Fatal("failed adapter did not fall back")
	}
	p.SetAdapters(nil)
	if _, err = c.Read(make([]byte, 1)); err == nil {
		t.Fatal("rescan kept a removed adapter connection")
	}
	p.Close()
	p.Close()
}

// A real local swarm proves custom-only dialing still downloads and verifies data.
func TestLocalTorrentDownloadUsesSelectedAdapterAndStorage(t *testing.T) {
	for _, source := range []string{"magnet", "file", "multi-magnet", "multi-file"} {
		t.Run(source, func(t *testing.T) { testLocalTorrentDownload(t, source) })
	}
}

func testLocalTorrentDownload(t *testing.T, source string) {
	t.Helper()
	mi, metainfoBytes := testInfo(t)
	seedDir := t.TempDir()
	payloadPath := filepath.Join(seedDir, "payload.bin")
	wantBytes, wantPieces := int64(4), 1
	if strings.HasPrefix(source, "multi-") {
		mi, metainfoBytes = multiFileInfo(t)
		name := "Marshals.S02E02.1080p.x265-ELiTE"
		if err := os.MkdirAll(filepath.Join(seedDir, name, "Subtitles"), 0755); err != nil {
			t.Fatal(err)
		}
		payloadPath = filepath.Join(seedDir, name, "video.bin")
		if err := os.WriteFile(filepath.Join(seedDir, name, "Subtitles", "en.srt"), []byte("more"), 0600); err != nil {
			t.Fatal(err)
		}
		wantBytes, wantPieces = 8, 2
	}
	if err := os.WriteFile(payloadPath, []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = seedDir
	seedStorage := storage.NewMMapWithCompletion(seedDir, storage.NewMapPieceCompletion())
	cfg.DefaultStorage = seedStorage
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.DisableIPv6 = true
	cfg.DisableUTP = true
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	cfg.Seed = true
	seeder, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { seeder.Close(); seedStorage.Close() }()
	seedSpec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatal(err)
	}
	seedTorrent, _, err := seeder.AddTorrentSpec(seedSpec)
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for seedTorrent.BytesCompleted() != wantBytes && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if seedTorrent.BytesCompleted() != wantBytes {
		t.Fatal("seed data did not verify")
	}
	e := testEngine(t)
	e.dialerPool.SetAdapters(loopAdapters()[:1])
	var id string
	if strings.HasSuffix(source, "file") {
		path := filepath.Join(t.TempDir(), "download.torrent")
		if err := os.WriteFile(path, metainfoBytes, 0600); err != nil {
			t.Fatal(err)
		}
		id, err = e.AddTorrentFile(path)
	} else {
		id, err = e.AddMagnet(mi.HashInfoBytes().HexString())
	}
	if err != nil {
		t.Fatal(err)
	}
	target := e.torrents[id].t
	target.AddPeers([]torrent.PeerInfo{{Addr: torrent.StringAddr(fmt.Sprintf("127.0.0.1:%d", seeder.LocalPort())), Trusted: true}})
	deadline = time.Now().Add(8 * time.Second)
	// Received bytes can be complete before the asynchronous piece hash finishes.
	for (target.BytesCompleted() != wantBytes || CountCompletedPieces(target) != wantPieces) && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if target.BytesCompleted() != wantBytes {
		t.Fatalf("custom dialer did not download: %+v", e.GetTorrents())
	}
	downloadDir := e.torrents[id].downloadDir
	if e.GetTorrents()[0].CompletedPieces != wantPieces {
		t.Fatal("piece completion was not recorded")
	}
	stats := e.dialerPool.GetAdapterStats()
	if stats[0].TotalConnections < 1 || stats[0].TotalDownloaded == 0 {
		t.Fatal("transfer bypassed selected adapter telemetry")
	}
	// Dropping must release real file handles before deletion or external reads.
	if err := e.RemoveTorrent(id, false); err != nil {
		t.Fatal(err)
	}
	payloadName := "payload.bin"
	if strings.HasPrefix(source, "multi-") {
		payloadName = "video.bin"
		if _, err := os.Stat(filepath.Join(downloadDir, filepath.Base(downloadDir))); !os.IsNotExist(err) {
			t.Fatal("download created duplicate torrent root")
		}
		if data, err := os.ReadFile(filepath.Join(downloadDir, "Subtitles", "en.srt")); err != nil || string(data) != "more" {
			t.Fatal("download lost internal directory structure")
		}
	}
	data, err := os.ReadFile(filepath.Join(downloadDir, payloadName))
	if err != nil || string(data) != "test" {
		t.Fatalf("download storage mismatch: %q %v", data, err)
	}
}

func TestLocalSeedingUploadSpeedAndRealFileDeletion(t *testing.T) {
	e := testEngine(t)
	e.dialerPool.SetAdapters(loopAdapters()[:1])
	mi, _ := testInfo(t)
	id := mi.HashInfoBytes().HexString()
	_, seedDir, err := e.storage.CreateTorrentDir(id)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(seedDir, "payload.bin"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg := torrent.NewDefaultClientConfig()
	destDir := t.TempDir()
	dst := storage.NewMMapWithCompletion(destDir, storage.NewMapPieceCompletion())
	cfg.DefaultStorage = dst
	cfg.ListenHost = func(string) string { return "127.0.0.1" }
	cfg.ListenPort = 0
	cfg.DisableIPv6 = true
	cfg.DisableUTP = true
	cfg.NoDHT = true
	cfg.NoDefaultPortForwarding = true
	downloader, err := torrent.NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { downloader.Close(); dst.Close() }()
	spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
	if err != nil {
		t.Fatal(err)
	}
	dest, _, err := downloader.AddTorrentSpec(spec)
	if err != nil {
		t.Fatal(err)
	}
	dest.DownloadAll()
	if _, err = e.AddMagnet(id); err != nil {
		t.Fatal(err)
	}
	e.torrents[id].t.AddPeers([]torrent.PeerInfo{{Addr: torrent.StringAddr(fmt.Sprintf("127.0.0.1:%d", downloader.LocalPort())), Trusted: true}})
	deadline := time.Now().Add(8 * time.Second)
	for dest.BytesCompleted() != 4 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if dest.BytesCompleted() != 4 {
		t.Fatal("existing downloaded data did not seed to the local peer")
	}
	e.mu.Lock()
	e.torrents[id].lastSampleAt = time.Now().Add(-time.Second)
	e.mu.Unlock()
	info := e.GetTorrents()[0]
	if info.Status != state.StatusSeeding || info.UploadedBytes != 4 || info.UploadSpeed <= 0 {
		t.Fatalf("invalid seeding telemetry: %+v", info)
	}
	if err = e.RemoveTorrent(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err = os.Stat(seedDir); !os.IsNotExist(err) {
		t.Fatalf("downloaded files remained locked or undeleted: %v", err)
	}
}

func TestTransferWaitsForVerificationBeforeSeeding(t *testing.T) {
	entry := &torrentEntry{}
	if got := transferStatus(entry, true, 4, 4, 0, 1); got != state.StatusChecking {
		t.Fatalf("unverified received data reported %s", got)
	}
	if got := transferStatus(entry, true, 4, 4, 1, 1); got != state.StatusSeeding {
		t.Fatalf("verified data reported %s", got)
	}
	entry.paused = true
	if got := transferStatus(entry, true, 4, 4, 0, 1); got != state.StatusPaused {
		t.Fatalf("verification changed paused status: %s", got)
	}
}

func TestResumeClearsWriteErrorAndPausedState(t *testing.T) {
	e := testEngine(t)
	id, err := e.AddMagnet(strings.Repeat("4", 40))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	e.torrents[id].lastError = "Cannot write downloaded data: disk full"
	e.mu.Unlock()
	if got := e.GetTorrents()[0]; got.Status != state.StatusError || got.Error == "" {
		t.Fatalf("disk error not surfaced: %+v", got)
	}
	if err := e.ResumeTorrent(id); err != nil {
		t.Fatal(err)
	}
	if got := e.GetTorrents()[0]; got.Status != state.StatusMetadata || got.Error != "" {
		t.Fatalf("retry did not clear the error and pause: %+v", got)
	}
}

func TestDiagnosticsKeepDistinctIDsForSamePeerAndAdapter(t *testing.T) {
	listener := echoListener(t)
	pool := NewDialerPool(loopAdapters()[:1], nil)
	t.Cleanup(pool.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for i := 0; i < 2; i++ {
		if _, err := pool.Dial(ctx, listener.Addr().String()); err != nil {
			t.Fatal(err)
		}
	}
	routes := pool.GetPeerDiagnostics()
	if len(routes) != 2 || routes[0].AdapterID != routes[1].AdapterID || routes[0].PeerIP != routes[1].PeerIP {
		t.Fatalf("expected same-peer connections on the same adapter: %+v", routes)
	}
	if routes[0].ID == "" || routes[0].ID == routes[1].ID {
		t.Fatalf("routing rows lack distinct socket IDs: %+v", routes)
	}
}
