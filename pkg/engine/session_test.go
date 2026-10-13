package engine

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

func sessionEngine(t *testing.T, path, downloadDir string) *Engine {
	t.Helper()
	root, err := NewStorageManager(downloadDir)
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
	saved, err := openSessionStore(path)
	if err != nil {
		client.Close()
		monitor.Stop()
		cancel()
		t.Fatal(err)
	}
	e := &Engine{client: client, storage: root, monitor: monitor, dialerPool: pool,
		torrents: make(map[string]*torrentEntry), ctx: ctx, cancel: cancel, session: saved}
	t.Cleanup(e.Close)
	e.restoreSession()
	return e
}

func readSession(t *testing.T, path string) *sessionStore {
	t.Helper()
	saved, err := openSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	return saved
}

func waitSessionCheck(t *testing.T, e *Engine, id string) *torrent.Torrent {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.RLock()
		entry := e.torrents[id]
		ready := entry != nil && entry.t.Info() != nil && !entry.checking
		e.mu.RUnlock()
		if ready {
			return entry.t
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("recovery did not finish checking: %+v; %s", e.GetTorrents(), e.GetRecoveryError())
	return nil
}

func TestSessionLifecycleAndFailedWrites(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "config", "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	id, err := e.AddMagnet(strings.Repeat("1", 40))
	if err != nil {
		t.Fatal(err)
	}
	record := readSession(t, path).records[id]
	if record.InfoHash != id || record.Source != "magnet:?xt=urn:btih:"+id ||
		record.AddedAt <= 0 || record.Paused || record.DownloadDir != e.GetTorrents()[0].DownloadDir {
		t.Fatalf("invalid saved transfer: %+v", record)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	if !readSession(t, path).records[id].Paused {
		t.Fatal("pause not saved")
	}
	if _, err := e.AddMagnet(id); err != nil {
		t.Fatal(err)
	}
	if duplicate := readSession(t, path).records[id]; duplicate.AddedAt != record.AddedAt || !duplicate.Paused {
		t.Fatal("duplicate changed saved time or pause")
	}
	if err := e.ResumeTorrent(id); err != nil {
		t.Fatal(err)
	}
	if readSession(t, path).records[id].Paused {
		t.Fatal("resume not saved")
	}

	// A directory at the temporary filename causes a deterministic write failure,
	// including on Windows where chmod cannot simulate a read-only file reliably.
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	original, _ := os.ReadFile(path)
	for _, operation := range []func() error{
		func() error { return e.PauseTorrent(id) },
		func() error { return e.RemoveTorrent(id, true) },
		func() error { _, err := e.AddMagnet(strings.Repeat("2", 40)); return err },
	} {
		if err := operation(); err == nil {
			t.Fatal("failed write was not surfaced")
		}
		disk, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(disk, original) {
			t.Fatal("failed write changed committed JSON")
		}
	}
	if transfers := e.GetTorrents(); len(transfers) != 1 || transfers[0].Status == state.StatusPaused {
		t.Fatalf("failed write changed live transfers: %+v", transfers)
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.ResumeTorrent(id); err == nil {
		t.Fatal("failed resume write was hidden")
	}
	if e.GetTorrents()[0].Status != state.StatusPaused {
		t.Fatal("failed write resumed paused transfer")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveTorrent(id, false); err != nil {
		t.Fatal(err)
	}
	if len(readSession(t, path).records) != 0 {
		t.Fatal("removal not saved")
	}
	e.Close()
	restarted := sessionEngine(t, path, filepath.Join(root, "new-downloads"))
	if len(restarted.GetTorrents()) != 0 {
		t.Fatal("removed transfer returned")
	}
}

func TestSessionInterruptedWriteAndMalformedState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	id := strings.Repeat("3", 40)
	record := sessionTorrent{InfoHash: id, Source: "magnet:?xt=urn:btih:" + id, AddedAt: 42,
		DownloadDir: filepath.Join(root, id)}
	saved, err := openSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.update(id, &record); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".tmp", []byte("[unfinished"), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readSession(t, path).records[id]; got.AddedAt != 42 {
		t.Fatal("interrupted write replaced committed data")
	}
	record.Paused = true
	if err := saved.update(id, &record); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Fatal("temporary file remained after commit")
	}
	original := []byte("[broken")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openSessionStore(path); err == nil {
		t.Fatal("accepted malformed JSON")
	}
	actual, _ := os.ReadFile(path)
	if !bytes.Equal(actual, original) {
		t.Fatal("malformed state was destroyed")
	}
	record.DownloadDir = filepath.Join(root, "wrong-directory")
	invalid, _ := json.Marshal([]sessionTorrent{record})
	if err := os.WriteFile(path, invalid, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := openSessionStore(path); err == nil {
		t.Fatal("accepted unsafe transfer directory")
	}
}

func twoPieceInfo(t *testing.T) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	first, second := sha1.Sum([]byte("test")), sha1.Sum([]byte("more"))
	hashes := append(first[:], second[:]...)
	raw, err := bencode.Marshal(metainfo.Info{Name: "payload.bin", Length: 8, PieceLength: 4, Pieces: hashes})
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: raw}
	var data bytes.Buffer
	if err := mi.Write(&data); err != nil {
		t.Fatal(err)
	}
	return mi, data.Bytes()
}

// This subprocess exits without Engine.Close or testing cleanups, proving that
// recovery does not depend on the normal shutdown callback.
func TestSessionAbruptExitHelper(t *testing.T) {
	root := os.Getenv("BONDO_TEST_ABRUPT_SESSION")
	if root == "" {
		return
	}
	e := sessionEngine(t, filepath.Join(root, "config", "session.json"), filepath.Join(root, "downloads"))
	if _, err := e.AddTorrentFile(filepath.Join(root, "source.torrent")); err != nil {
		t.Fatal(err)
	}
	id, err := e.AddMagnet(strings.Repeat("4", 40))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	os.Exit(0)
}

func TestSessionAbruptExitRecoversAndDownloadsOnlyMissingPieces(t *testing.T) {
	root := t.TempDir()
	mi, metainfoBytes := twoPieceInfo(t)
	id := mi.HashInfoBytes().HexString()
	dir := filepath.Join(root, "downloads", id)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("testmore"), 0600); err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "source.torrent")
	if err := os.WriteFile(source, metainfoBytes, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestSessionAbruptExitHelper$")
	child.Env = append(os.Environ(), "BONDO_TEST_ABRUPT_SESSION="+root)
	if output, err := child.CombinedOutput(); err != nil {
		t.Fatalf("abrupt helper: %v\n%s", err, output)
	}
	path := filepath.Join(root, "config", "session.json")
	before := readSession(t, path).records[id]
	if len(before.Metainfo) == 0 || before.Source != source {
		t.Fatal("file metadata/source not saved")
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	// Simulate a piece lost/corrupted during shutdown. Its prior completion must
	// not be trusted; the first piece must remain verified without redownloading.
	if err := os.WriteFile(filepath.Join(dir, "payload.bin"), []byte("testFAIL"), 0600); err != nil {
		t.Fatal(err)
	}
	e := sessionEngine(t, path, filepath.Join(root, "different-default"))
	target := waitSessionCheck(t, e, id)
	if CountCompletedPieces(target) != 1 || !target.Piece(0).State().Complete || target.Piece(1).State().Complete {
		t.Fatalf("existing pieces not checked independently: %+v", target.PieceStateRuns())
	}
	transfers := e.GetTorrents()
	if len(transfers) != 2 || transfers[0].DownloadDir != dir || transfers[0].AddedAt != before.AddedAt {
		// Equal second timestamps sort by hash, so find this transfer explicitly.
		found := false
		for _, transfer := range transfers {
			if transfer.ID == id && transfer.DownloadDir == dir && transfer.AddedAt == before.AddedAt {
				found = true
			}
		}
		if len(transfers) != 2 || !found {
			t.Fatalf("recovery changed transfer details: %+v", transfers)
		}
	}
	pausedID := strings.Repeat("4", 40)
	if got := e.GetTorrents(); len(got) != 2 {
		t.Fatal("paused transfer missing")
	}
	e.mu.RLock()
	paused := e.torrents[pausedID].paused
	e.mu.RUnlock()
	if !paused {
		t.Fatal("paused transfer auto-resumed")
	}
	e.dialerPool.SetAdapters(loopAdapters()[:1])

	seedDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(seedDir, "payload.bin"), []byte("testmore"), 0600); err != nil {
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
	for CountCompletedPieces(seedTorrent) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if CountCompletedPieces(seedTorrent) != 2 {
		t.Fatal("seed did not verify")
	}
	target.AddPeers([]torrent.PeerInfo{{Addr: torrent.StringAddr(fmt.Sprintf("127.0.0.1:%d", seeder.LocalPort())), Trusted: true}})
	deadline = time.Now().Add(8 * time.Second)
	for CountCompletedPieces(target) != 2 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if CountCompletedPieces(target) != 2 {
		t.Fatalf("missing piece not recovered: %+v", e.GetTorrents())
	}
	downloadStats := target.Stats()
	if received := downloadStats.BytesReadUsefulData.Int64(); received != 4 {
		t.Fatalf("downloaded %d useful bytes; expected only the missing 4-byte piece", received)
	}
	e.Close()
	content, err := os.ReadFile(filepath.Join(dir, "payload.bin"))
	if err != nil || string(content) != "testmore" {
		t.Fatalf("recovered payload: %q %v", content, err)
	}
	again := sessionEngine(t, path, filepath.Join(root, "another-default"))
	complete := waitSessionCheck(t, again, id)
	if CountCompletedPieces(complete) != 2 {
		t.Fatal("completed transfer did not recover")
	}
	for _, transfer := range again.GetTorrents() {
		if transfer.ID == id && transfer.Status != state.StatusSeeding {
			t.Fatalf("completed transfer status: %s", transfer.Status)
		}
		if transfer.ID == pausedID && transfer.Status != state.StatusPaused {
			t.Fatal("pause not preserved across second restart")
		}
	}
}

func TestSessionMagnetMetadataCacheAndConcurrentMutations(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	mi, _ := testInfo(t)
	id, err := e.AddMagnet(mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	if err := e.torrents[id].t.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.RLock()
		cached := len(e.session.records[id].Metainfo) > 0
		e.mu.RUnlock()
		if cached {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if len(readSession(t, path).records[id].Metainfo) == 0 {
		t.Fatal("resolved magnet metadata not cached")
	}
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Go(func() {
			if err := e.ResumeTorrent(id); err != nil {
				t.Error(err)
			}
			if err := e.PauseTorrent(id); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	e.Close()
	restarted := sessionEngine(t, path, filepath.Join(root, "new"))
	pausedTorrent := waitSessionCheck(t, restarted, id)
	if pausedTorrent.Piece(0).State().Priority != torrent.PiecePriorityNone {
		t.Fatal("recovery requested paused data")
	}
	if got := restarted.GetTorrents()[0]; got.Status != state.StatusPaused || got.AddedAt != readSession(t, path).records[id].AddedAt {
		t.Fatalf("cached magnet pause not preserved: %+v", got)
	}
}

func TestSessionFailedTransferDoesNotEraseOtherRecords(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	saved, _ := openSessionStore(path)
	badID, goodID := strings.Repeat("5", 40), strings.Repeat("6", 40)
	bad := sessionTorrent{InfoHash: badID, Source: "magnet:?xt=urn:btih:" + badID, AddedAt: 1, DownloadDir: filepath.Join(root, "downloads", badID), Metainfo: []byte("broken")}
	good := sessionTorrent{InfoHash: goodID, Source: "magnet:?xt=urn:btih:" + goodID, AddedAt: 2, Paused: true, DownloadDir: filepath.Join(root, "downloads", goodID)}
	if err := saved.update(badID, &bad); err != nil {
		t.Fatal(err)
	}
	if err := saved.update(goodID, &good); err != nil {
		t.Fatal(err)
	}
	e := sessionEngine(t, path, filepath.Join(root, "other"))
	if len(e.GetTorrents()) != 1 || e.GetRecoveryError() == "" {
		t.Fatal("failed entry blocked healthy restoration or warning missing")
	}
	if err := e.ResumeTorrent(goodID); err != nil {
		t.Fatal(err)
	}
	if len(readSession(t, path).records) != 2 {
		t.Fatal("saving healthy transfer erased failed record")
	}
}

func TestSessionCheckingStatusPreservesPause(t *testing.T) {
	entry := &torrentEntry{checking: true}
	if transferStatus(entry, true, 4, 8, 1, 2) != state.StatusChecking {
		t.Fatal("recovery did not report Checking")
	}
	entry.paused = true
	if transferStatus(entry, true, 4, 8, 1, 2) != state.StatusPaused {
		t.Fatal("checking replaced paused status")
	}
}

func TestNewEngineRestoresDefaultConfigurationSession(t *testing.T) {
	config := t.TempDir()
	switch runtime.GOOS {
	case "windows":
		t.Setenv("APPDATA", config)
	case "darwin":
		t.Setenv("HOME", config)
	default:
		t.Setenv("XDG_CONFIG_HOME", config)
	}
	path, err := defaultSessionPath()
	if err != nil {
		t.Fatal(err)
	}
	mi, data := testInfo(t)
	id := mi.HashInfoBytes().HexString()
	record := sessionTorrent{InfoHash: id, Source: filepath.Join(config, "deleted.torrent"),
		AddedAt: 1234567890, Paused: true, DownloadDir: filepath.Join(config, "original-downloads", id), Metainfo: data}
	store, err := openSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.update(id, &record); err != nil {
		t.Fatal(err)
	}
	e, err := NewEngine(filepath.Join(config, "changed-default"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(e.Close)
	waitSessionCheck(t, e, id)
	got := e.GetTorrents()
	if len(got) != 1 || got[0].ID != id || got[0].Status != state.StatusPaused ||
		got[0].AddedAt != record.AddedAt || got[0].DownloadDir != record.DownloadDir {
		t.Fatalf("NewEngine did not automatically restore configuration session: %+v", got)
	}
}

func TestSessionRemoveDuringInitialChecking(t *testing.T) {
	root := t.TempDir()
	mi, data := twoPieceInfo(t)
	id := mi.HashInfoBytes().HexString()
	record := sessionTorrent{InfoHash: id, Source: filepath.Join(root, "missing.torrent"),
		AddedAt: 1, DownloadDir: filepath.Join(root, "downloads", id), Metainfo: data}
	path := filepath.Join(root, "session.json")
	saved, err := openSessionStore(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := saved.update(id, &record); err != nil {
		t.Fatal(err)
	}
	e := sessionEngine(t, path, filepath.Join(root, "other"))
	if err := e.RemoveTorrent(id, true); err != nil {
		t.Fatal(err)
	}
	e.Close()
	if len(readSession(t, path).records) != 0 {
		t.Fatal("checking worker resurrected removed torrent")
	}
}

func TestSessionCachedMagnetKeepsDiscoveryHints(t *testing.T) {
	e := testEngine(t)
	mi, data := testInfo(t)
	id := mi.HashInfoBytes().HexString()
	source := "MAGNET:?xt=urn:btih:" + id + "&tr=http%3A%2F%2F127.0.0.1%3A1%2Fannounce&x.pe=127.0.0.1%3A7777"
	spec, err := e.sessionSpec(sessionTorrent{InfoHash: id, Source: source, Metainfo: data})
	if err != nil {
		t.Fatal(err)
	}
	if len(spec.PeerAddrs) != 1 || spec.PeerAddrs[0] != "127.0.0.1:7777" || len(spec.Trackers) == 0 {
		t.Fatalf("lost original magnet discovery hints: %+v", spec)
	}
}

func TestSessionCachedMagnetMetadataExceedsFileImportLimit(t *testing.T) {
	e := testEngine(t)
	sum := sha1.Sum([]byte("test"))
	raw, err := bencode.Marshal(map[string]any{
		"name": "payload.bin", "length": int64(4), "piece length": int64(4),
		"pieces": string(sum[:]), "extension": strings.Repeat("x", maxMetainfoBytes),
	})
	if err != nil {
		t.Fatal(err)
	}
	mi := &metainfo.MetaInfo{InfoBytes: raw}
	var data bytes.Buffer
	if err := mi.Write(&data); err != nil {
		t.Fatal(err)
	}
	if _, err := readMetainfoSpec(bytes.NewReader(data.Bytes())); err == nil {
		t.Fatal("external torrent file import limit was removed")
	}
	id := mi.HashInfoBytes().HexString()
	spec, err := e.sessionSpec(sessionTorrent{InfoHash: id, Source: "magnet:?xt=urn:btih:" + id, Metainfo: data.Bytes()})
	if err != nil || torrentSpecID(spec) != id {
		t.Fatalf("valid cached magnet failed recovery: %v", err)
	}
}
