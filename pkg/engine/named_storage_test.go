package engine

import (
	"bytes"
	"crypto/sha1"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/bencode"
	"github.com/anacrolix/torrent/metainfo"
	"torrent-multipath/pkg/state"
)

func namedInfo(t *testing.T, name, payload string) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	sum := sha1.Sum([]byte(payload))
	raw, err := bencode.Marshal(metainfo.Info{Name: name, Length: int64(len(payload)), PieceLength: int64(len(payload)), Pieces: sum[:]})
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

func waitNamedFolder(t *testing.T, e *Engine, id string) string {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		e.mu.RLock()
		entry := e.torrents[id]
		ready := entry != nil && !entry.namePending && len(entry.metainfo) > 0
		var dir string
		if entry != nil {
			dir = entry.downloadDir
		}
		e.mu.RUnlock()
		if ready {
			return dir
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("named folder not finalized: %+v", e.GetTorrents())
	return ""
}

func TestNamedFolderCollisionsOwnershipAndReAdd(t *testing.T) {
	s, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	base := s.GetBaseDir()
	userDir := filepath.Join(base, "Movie")
	if err := os.Mkdir(userDir, 0755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(userDir, "keep.txt")
	if err := os.WriteFile(keep, []byte("user file"), 0600); err != nil {
		t.Fatal(err)
	}
	firstID, secondID := strings.Repeat("1", 40), strings.Repeat("2", 40)
	first, named, err := s.createNamedTorrentDir(base, firstID, "Movie")
	if err != nil || !named || filepath.Base(first) != "Movie (2)" {
		t.Fatalf("first collision: %s %v", first, err)
	}
	second, _, err := s.createNamedTorrentDir(base, secondID, "Movie")
	if err != nil || filepath.Base(second) != "Movie (3)" {
		t.Fatalf("second collision: %s %v", second, err)
	}
	again, _, err := s.createNamedTorrentDir(base, firstID, "Movie")
	if err != nil || again != first {
		t.Fatalf("same torrent did not reuse folder: %s %v", again, err)
	}
	if err := s.RemoveTorrentFiles(base, firstID, second); err == nil {
		t.Fatal("deleted another torrent's folder")
	}
	if err := s.RemoveTorrentFiles(base, firstID, userDir); err == nil {
		t.Fatal("deleted user folder")
	}
	if err := s.RemoveTorrentFiles(base, firstID, filepath.Join(base, "..", "outside")); err == nil {
		t.Fatal("accepted escaped deletion")
	}
	if err := s.RemoveTorrentFiles(base, firstID, first); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(second); err != nil {
		t.Fatal("deletion affected another torrent")
	}
	if data, err := os.ReadFile(keep); err != nil || string(data) != "user file" {
		t.Fatal("user folder changed")
	}
}

func TestNamedFolderConcurrentAllocationAndWindowsNames(t *testing.T) {
	s, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"../CON", "CON", "LPT1.txt", "COM¹.bin", "movie:bad?name*", "සිංහල Movie", strings.Repeat("x", 400), "..", ""} {
		folder := torrentFolderName(name)
		if !validTorrentFolderLeaf(folder) || len([]rune(folder)) > 101 {
			t.Fatalf("unsafe folder for %q: %q", name, folder)
		}
	}
	var wg sync.WaitGroup
	dirs := make(chan string, 8)
	for i := 1; i <= 8; i++ {
		id := strings.Repeat(string(rune('0'+i)), 40)
		wg.Go(func() {
			dir, _, err := s.createNamedTorrentDir(s.GetBaseDir(), id, "Movie")
			if err != nil {
				t.Error(err)
				return
			}
			dirs <- dir
		})
	}
	wg.Wait()
	close(dirs)
	seen := map[string]bool{}
	for dir := range dirs {
		if seen[dir] {
			t.Fatal("concurrent torrents shared a folder")
		}
		seen[dir] = true
	}
	if len(seen) != 8 {
		t.Fatalf("allocated %d of 8 folders", len(seen))
	}
}

func TestNamedTorrentFilesRecoverPauseAndVerifyExistingData(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	sources := []string{filepath.Join(root, "a.torrent"), filepath.Join(root, "b.torrent")}
	ids := make([]string, 2)
	for i, payload := range []string{"test", "more"} {
		mi, data := namedInfo(t, "Movie", payload)
		if err := os.WriteFile(sources[i], data, 0600); err != nil {
			t.Fatal(err)
		}
		id, err := e.AddTorrentFile(sources[i])
		if err != nil || id != mi.HashInfoBytes().HexString() {
			t.Fatalf("add: %s %v", id, err)
		}
		ids[i] = id
		if err := e.PauseTorrent(id); err != nil {
			t.Fatal(err)
		}
	}
	dirs := []string{waitNamedFolder(t, e, ids[0]), waitNamedFolder(t, e, ids[1])}
	if filepath.Base(dirs[0]) != "Movie" || filepath.Base(dirs[1]) != "Movie (2)" {
		t.Fatalf("named folders: %v", dirs)
	}
	original := readSession(t, path).records[ids[0]]
	if !original.NamedFolder || original.NamePending || original.DownloadDir != dirs[0] {
		t.Fatalf("saved named path: %+v", original)
	}
	duplicate, err := e.AddTorrentFile(sources[0])
	if err != nil || duplicate != ids[0] || e.GetTorrents()[0].Status != state.StatusPaused {
		t.Fatalf("duplicate resumed/replaced torrent: %v", err)
	}
	e.Close()
	for i, payload := range []string{"test", "more"} {
		if err := os.WriteFile(filepath.Join(dirs[i], "Movie"), []byte(payload), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(sources[i]); err != nil {
			t.Fatal(err)
		}
	}
	restarted := sessionEngine(t, path, filepath.Join(root, "new-default"))
	for i, id := range ids {
		target := waitSessionCheck(t, restarted, id)
		if CountCompletedPieces(target) != 1 || target.Piece(0).State().Priority != torrent.PiecePriorityNone {
			t.Fatal("paused recovery did not verify existing data")
		}
		saved := readSession(t, path).records[id]
		if saved.DownloadDir != dirs[i] || !saved.Paused {
			t.Fatal("recovery changed folder or pause")
		}
	}
	if readSession(t, path).records[ids[0]].AddedAt != original.AddedAt {
		t.Fatal("timestamp changed")
	}
	if err := restarted.ResumeTorrent(ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := restarted.RemoveTorrent(ids[0], true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dirs[0]); !os.IsNotExist(err) {
		t.Fatal("own named folder not deleted")
	}
	if _, err := os.Stat(filepath.Join(dirs[1], "Movie")); err != nil {
		t.Fatal("other transfer's files were removed")
	}
}

func TestPendingMagnetUsesMetadataNameAndRecoversAllocation(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	mi, _ := namedInfo(t, "Actual Movie", "test")
	id, err := e.AddMagnet("magnet:?xt=urn:btih:" + mi.HashInfoBytes().HexString() + "&dn=WrongHint")
	if err != nil {
		t.Fatal(err)
	}
	record := readSession(t, path).records[id]
	if !record.NamePending || !record.NamedFolder {
		t.Fatal("unresolved magnet not saved as pending")
	}
	if _, err := os.Stat(record.DownloadDir); !os.IsNotExist(err) {
		t.Fatal("created visible hash folder before metadata")
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	e.Close()
	// Reproduce a power loss after folder allocation but before session updating.
	// The persisted pending record must find the same owned folder, not "(2)".
	manager, err := NewStorageManager(filepath.Dir(record.DownloadDir))
	if err != nil {
		t.Fatal(err)
	}
	allocated, _, err := manager.createNamedTorrentDir(manager.GetBaseDir(), id, "Actual Movie")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(allocated, "Actual Movie"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	restored := sessionEngine(t, path, filepath.Join(root, "other"))
	if err := restored.torrents[id].t.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	target := waitSessionCheck(t, restored, id)
	dir := waitNamedFolder(t, restored, id)
	if dir != allocated || filepath.Base(dir) != "Actual Movie" {
		t.Fatalf("metadata folder changed: %s", dir)
	}
	if CountCompletedPieces(target) != 1 || target.Piece(0).State().Priority != torrent.PiecePriorityNone {
		t.Fatal("paused pending magnet did not recover existing data")
	}
	if saved := readSession(t, path).records[id]; saved.NamePending || !saved.Paused || saved.DownloadDir != allocated {
		t.Fatalf("pending state not finalized: %+v", saved)
	}
}

func TestNamedFolderRejectsSymlinkAndUnownedSessionPath(t *testing.T) {
	root := t.TempDir()
	s, err := NewStorageManager(filepath.Join(root, "downloads"))
	if err != nil {
		t.Fatal(err)
	}
	id := strings.Repeat("7", 40)
	unowned := filepath.Join(s.GetBaseDir(), "Movie")
	if err := os.Mkdir(unowned, 0755); err != nil {
		t.Fatal(err)
	}
	if err := prepareRecordedTorrentDir(s.GetBaseDir(), unowned, id, true); err == nil {
		t.Fatal("claimed existing user folder from saved state")
	}
	outside := t.TempDir()
	link := filepath.Join(s.GetBaseDir(), "Linked")
	if err := os.Symlink(outside, link); err == nil {
		if err := prepareRecordedTorrentDir(s.GetBaseDir(), link, id, true); err == nil {
			t.Fatal("accepted linked restoration folder")
		}
		if err := s.RemoveTorrentFiles(s.GetBaseDir(), id, link); err == nil {
			t.Fatal("accepted linked deletion")
		}
	}
}

func TestNamedMagnetSessionWriteFailureCanRetry(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	mi, _ := namedInfo(t, "Movie", "test")
	id, err := e.AddMagnet(mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := e.torrents[id].t.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	failed := false
	for time.Now().Before(deadline) {
		info := e.GetTorrents()[0]
		if info.Status == state.StatusError {
			failed = true
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !failed {
		t.Fatal("session write failure was not surfaced")
	}
	if e.torrents[id].t.Piece(0).State().Priority != torrent.PiecePriorityNone {
		t.Fatal("download began before named path was persisted")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := e.ResumeTorrent(id); err != nil {
		t.Fatal(err)
	}
	saved := readSession(t, path).records[id]
	if saved.NamePending || !saved.NamedFolder || filepath.Base(saved.DownloadDir) != "Movie" {
		t.Fatalf("retry did not save final folder: %+v", saved)
	}
	if e.torrents[id].t.Piece(0).State().Priority == torrent.PiecePriorityNone {
		t.Fatal("retry did not request incomplete data")
	}
}

func TestRemovePendingNamedMagnetDeletesNoUnrelatedData(t *testing.T) {
	root := t.TempDir()
	e := sessionEngine(t, filepath.Join(root, "session.json"), filepath.Join(root, "downloads"))
	keep := filepath.Join(e.storage.GetBaseDir(), "keep.txt")
	if err := os.WriteFile(keep, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	id, err := e.AddMagnet(strings.Repeat("9", 40))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.RemoveTorrent(id, true); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatal("pending removal deleted unrelated files")
	}
	if len(readSession(t, filepath.Join(root, "session.json")).records) != 0 {
		t.Fatal("pending transfer stayed in session")
	}
}

func TestNamedReAddReusesSuffixAfterEarlierFolderIsDeleted(t *testing.T) {
	s, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	first, _, err := s.createNamedTorrentDir(s.GetBaseDir(), firstID, "Movie")
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := s.createNamedTorrentDir(s.GetBaseDir(), secondID, "Movie")
	if err != nil {
		t.Fatal(err)
	}
	payload := filepath.Join(second, "keep.bin")
	if err := os.WriteFile(payload, []byte("partial download"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.RemoveTorrentFiles(s.GetBaseDir(), firstID, first); err != nil {
		t.Fatal(err)
	}
	again, _, err := s.createNamedTorrentDir(s.GetBaseDir(), secondID, "Movie")
	if err != nil || again != second {
		t.Fatalf("re-add abandoned existing suffixed folder: %s %v", again, err)
	}
	if data, err := os.ReadFile(payload); err != nil || string(data) != "partial download" {
		t.Fatal("existing payload changed")
	}
}

func TestNamedFolderCannotImpersonateAnotherLegacyHashFolder(t *testing.T) {
	s, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	firstID, secondID := strings.Repeat("a", 40), strings.Repeat("b", 40)
	// A valid torrent name can be the 40-character hash of another torrent.
	named, _, err := s.createNamedTorrentDir(s.GetBaseDir(), secondID, firstID)
	if err != nil {
		t.Fatal(err)
	}
	actual, _, err := s.createNamedTorrentDir(s.GetBaseDir(), firstID, "Movie")
	if err != nil || actual == named {
		t.Fatalf("shared another torrent's named folder: %s %v", actual, err)
	}
	if err := prepareRecordedTorrentDir(s.GetBaseDir(), named, firstID, false); err == nil {
		t.Fatal("legacy restoration claimed another torrent's folder")
	}
	if err := s.RemoveTorrentFiles(s.GetBaseDir(), firstID, named); err == nil {
		t.Fatal("hash-shaped name bypassed ownership check")
	}
	if _, err := os.Stat(named); err != nil {
		t.Fatal("other torrent's folder was removed")
	}
}

func TestEngineDoesNotCreateDefaultCompletionDatabase(t *testing.T) {
	root := t.TempDir()
	downloadDir := filepath.Join(root, "downloads")
	sessionPath := filepath.Join(root, "session.json")
	assertNoDatabase := func() {
		t.Helper()
		err := filepath.WalkDir(downloadDir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.Name() == ".torrent.bolt.db" || entry.Name() == ".torrent.db" {
				t.Errorf("unused database created: %s", path)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	e := sessionEngine(t, sessionPath, downloadDir)
	assertNoDatabase()
	_, data := namedInfo(t, "Movie", "test")
	source := filepath.Join(root, "source.torrent")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	id, err := e.AddTorrentFile(source)
	if err != nil {
		t.Fatal(err)
	}
	waitNamedFolder(t, e, id)
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	assertNoDatabase()
	e.Close()
	restarted := sessionEngine(t, sessionPath, downloadDir)
	waitSessionCheck(t, restarted, id)
	if got := restarted.GetTorrents()[0]; got.Status != state.StatusPaused {
		t.Fatal("database removal changed saved pause state")
	}
	assertNoDatabase()
	restarted.Close()
	assertNoDatabase()
}

func multiFileInfo(t *testing.T) (*metainfo.MetaInfo, []byte) {
	t.Helper()
	first, second := sha1.Sum([]byte("test")), sha1.Sum([]byte("more"))
	raw, err := bencode.Marshal(metainfo.Info{
		Name: "Fallback", NameUtf8: "Marshals.S02E02.1080p.x265-ELiTE",
		PieceLength: 4, Pieces: append(first[:], second[:]...),
		Files: []metainfo.FileInfo{
			{Length: 4, Path: []string{"video.bin"}},
			{Length: 4, Path: []string{"Subtitles", "en.srt"}},
		},
	})
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

func TestNamedMultiFileRecoveryUsesOneRootAndPreservesMetadata(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "session.json")
	e := sessionEngine(t, path, filepath.Join(root, "downloads"))
	mi, data := multiFileInfo(t)
	source := filepath.Join(root, "source.torrent")
	if err := os.WriteFile(source, data, 0600); err != nil {
		t.Fatal(err)
	}
	id, err := e.AddTorrentFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := e.PauseTorrent(id); err != nil {
		t.Fatal(err)
	}
	dir := waitNamedFolder(t, e, id)
	if filepath.Base(dir) != "Marshals.S02E02.1080p.x265-ELiTE" {
		t.Fatalf("incorrect outer name: %s", dir)
	}
	if got := e.torrents[id].t.Info(); got.Name != "Fallback" || got.NameUtf8 != filepath.Base(dir) {
		t.Fatal("storage modified original metadata")
	}
	e.Close()
	if err := os.WriteFile(filepath.Join(dir, "video.bin"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "Subtitles", "en.srt"), []byte("more"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(source); err != nil {
		t.Fatal(err)
	}
	restarted := sessionEngine(t, path, filepath.Join(root, "new-default"))
	target := waitSessionCheck(t, restarted, id)
	if CountCompletedPieces(target) != 2 || target.Piece(0).State().Priority != torrent.PiecePriorityNone {
		t.Fatal("flat data or saved pause state not recovered")
	}
	if _, err := os.Stat(filepath.Join(dir, filepath.Base(dir))); !os.IsNotExist(err) {
		t.Fatal("duplicate root created")
	}
	restarted.Close()
	// Re-adding without a session must also retain the flat layout marker.
	e = sessionEngine(t, filepath.Join(root, "empty-session.json"), filepath.Dir(dir))
	id, err = e.AddMagnet(mi.HashInfoBytes().HexString())
	if err != nil {
		t.Fatal(err)
	}
	if err := e.torrents[id].t.SetInfoBytes(mi.InfoBytes); err != nil {
		t.Fatal(err)
	}
	if again := waitNamedFolder(t, e, id); again != dir {
		t.Fatal("re-add abandoned original files")
	}
	if err := waitForInitialChecks(e.ctx, e.torrents[id].t); err != nil {
		t.Fatal(err)
	}
	if CountCompletedPieces(e.torrents[id].t) != 2 {
		t.Fatal("re-add did not hash existing flat files")
	}
}

func TestOldNamedMultiFileFoldersMigrateAndRecoverInterruptedMoves(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(fmt.Sprint(interrupted), func(t *testing.T) {
			root := t.TempDir()
			manager, err := NewStorageManager(filepath.Join(root, "downloads"))
			if err != nil {
				t.Fatal(err)
			}
			mi, data := multiFileInfo(t)
			id := mi.HashInfoBytes().HexString()
			name := "Marshals.S02E02.1080p.x265-ELiTE"
			dir, _, err := manager.createNamedTorrentDir(manager.GetBaseDir(), id, name)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(ownershipFile(dir, id), []byte(id+"\n"), 0600); err != nil {
				t.Fatal(err)
			}
			nested := filepath.Join(dir, name)
			if err := os.MkdirAll(filepath.Join(nested, "Subtitles"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "video.bin"), []byte("test"), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(nested, "Subtitles", "en.srt"), []byte("more"), 0600); err != nil {
				t.Fatal(err)
			}
			if interrupted {
				if err := os.Rename(filepath.Join(nested, "video.bin"), filepath.Join(dir, "video.bin")); err != nil {
					t.Fatal(err)
				}
			}
			path := filepath.Join(root, "session.json")
			saved, err := openSessionStore(path)
			if err != nil {
				t.Fatal(err)
			}
			record := sessionTorrent{InfoHash: id, Source: "magnet:?xt=urn:btih:" + id,
				AddedAt: 123, Paused: true, DownloadDir: dir, NamedFolder: true, Metainfo: data}
			if err := saved.update(id, &record); err != nil {
				t.Fatal(err)
			}
			e := sessionEngine(t, path, filepath.Join(root, "new-default"))
			target := waitSessionCheck(t, e, id)
			if CountCompletedPieces(target) != 2 || target.Piece(0).State().Priority != torrent.PiecePriorityNone {
				t.Fatal("migration lost payload or pause")
			}
			if _, err := os.Stat(nested); !os.IsNotExist(err) {
				t.Fatal("obsolete nested root remains")
			}
			if owned, flat := readFolderOwnership(dir, id); !owned || !flat {
				t.Fatal("migration layout not recorded")
			}
			e.Close()
			e = sessionEngine(t, path, manager.GetBaseDir())
			if CountCompletedPieces(waitSessionCheck(t, e, id)) != 2 {
				t.Fatal("second restart lost migrated files")
			}
		})
	}
}

func TestFlatMigrationPreservesCollisionsAndRejectsOwnershipPayload(t *testing.T) {
	manager, err := NewStorageManager(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	mi, _ := multiFileInfo(t)
	info, err := mi.UnmarshalInfo()
	if err != nil {
		t.Fatal(err)
	}
	id := mi.HashInfoBytes().HexString()
	dir, _, err := manager.createNamedTorrentDir(manager.GetBaseDir(), id, info.BestName())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ownershipFile(dir, id), []byte(id+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	nested := filepath.Join(dir, info.BestName())
	if err := os.Mkdir(nested, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nested, "video.bin"), []byte("test"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "video.bin"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := prepareFlatTorrentDirectory(dir, id, &info); err == nil {
		t.Fatal("overwrote a conflicting file")
	}
	for path, want := range map[string]string{
		filepath.Join(dir, "video.bin"):    "keep",
		filepath.Join(nested, "video.bin"): "test",
	} {
		data, err := os.ReadFile(path)
		if err != nil || string(data) != want {
			t.Fatal("migration collision changed data")
		}
	}
	info.Files[0].Path = []string{".bondo-torrent-" + id}
	if err := prepareFlatTorrentDirectory(dir, id, &info); err == nil {
		t.Fatal("accepted ownership marker as torrent payload")
	}
}
