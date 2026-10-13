package engine

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

// Metadata is cached with the source so moved/deleted .torrent files and offline URLs
// do not prevent recovery. Piece completion is deliberately never persisted.
type sessionTorrent struct {
	InfoHash    string `json:"infoHash"`
	Source      string `json:"source"`
	AddedAt     int64  `json:"addedAt"`
	Paused      bool   `json:"paused"`
	DownloadDir string `json:"downloadDir"`
	Metainfo    []byte `json:"metainfo,omitempty"`
	NamedFolder bool   `json:"namedFolder,omitempty"`
	NamePending bool   `json:"namePending,omitempty"`
}

type sessionStore struct {
	path    string
	records map[string]sessionTorrent
}

func defaultSessionPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "Bondo", "session.json"), nil
}

func openSessionStore(path string) (*sessionStore, error) {
	s := &sessionStore{path: path, records: make(map[string]sessionTorrent)}
	file, err := os.Open(path)
	if os.IsNotExist(err) {
		return s, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	defer file.Close()
	// Bound damaged or unexpectedly large input without discarding the original.
	const maxSessionBytes = 64 << 20
	data, err := io.ReadAll(io.LimitReader(file, maxSessionBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read session: %w", err)
	}
	if len(data) > maxSessionBytes {
		return nil, fmt.Errorf("session exceeds 64 MB")
	}
	var records []sessionTorrent
	if err := json.Unmarshal(data, &records); err != nil {
		return nil, fmt.Errorf("invalid session %s (file preserved): %w", path, err)
	}
	for _, record := range records {
		if !torrentIDPattern.MatchString(record.InfoHash) || strings.TrimSpace(record.Source) == "" ||
			record.AddedAt < 0 || !filepath.IsAbs(record.DownloadDir) ||
			(!record.NamedFolder && filepath.Base(filepath.Clean(record.DownloadDir)) != record.InfoHash) ||
			!validTorrentFolderLeaf(filepath.Base(filepath.Clean(record.DownloadDir))) ||
			(record.NamePending && (!record.NamedFolder || filepath.Base(filepath.Clean(record.DownloadDir)) != record.InfoHash)) {
			return nil, fmt.Errorf("invalid session transfer %q (file preserved)", record.InfoHash)
		}
		if _, duplicate := s.records[record.InfoHash]; duplicate {
			return nil, fmt.Errorf("duplicate session transfer %s (file preserved)", record.InfoHash)
		}
		s.records[record.InfoHash] = record
	}
	return s, nil
}

func orderedSession(records map[string]sessionTorrent) []sessionTorrent {
	list := make([]sessionTorrent, 0, len(records))
	for _, record := range records {
		list = append(list, record)
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].AddedAt == list[j].AddedAt {
			return list[i].InfoHash < list[j].InfoHash
		}
		return list[i].AddedAt < list[j].AddedAt
	})
	return list
}

// Engine.mu serializes all store mutations. A failed write leaves the old state intact.
func (s *sessionStore) update(id string, record *sessionTorrent) error {
	next := make(map[string]sessionTorrent, len(s.records)+1)
	for key, saved := range s.records {
		next[key] = saved
	}
	if record == nil {
		delete(next, id)
	} else {
		next[id] = *record
	}
	data, err := json.MarshalIndent(orderedSession(next), "", "  ")
	if err != nil {
		return err
	}
	if len(data) > 64<<20 {
		return fmt.Errorf("session exceeds 64 MB")
	}
	if err = writeSessionAtomic(s.path, append(data, '\n')); err != nil {
		return fmt.Errorf("save session: %w", err)
	}
	s.records = next
	return nil
}

func writeSessionAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	temp := path + ".tmp"
	file, err := os.OpenFile(temp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer os.Remove(temp)
	if _, err = file.Write(data); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return replaceSessionFile(temp, path)
}

func torrentSpecID(spec *torrent.TorrentSpec) string {
	if spec.InfoHash == ([20]byte{}) && spec.InfoHashV2.Ok {
		spec.InfoHash = *spec.InfoHashV2.Value.ToShort()
	}
	return spec.InfoHash.HexString()
}

func (e *Engine) saveEntryLocked(id string, entry *torrentEntry) error {
	if e.session == nil {
		return nil
	} // Nonpersistent embedded/test engines.
	record := entry.sessionRecord(id)
	return e.session.update(id, &record)
}

func (entry *torrentEntry) sessionRecord(id string) sessionTorrent {
	return sessionTorrent{InfoHash: id, Source: entry.source, AddedAt: entry.addedAt,
		Paused: entry.paused, DownloadDir: entry.downloadDir, Metainfo: entry.metainfo,
		NamedFolder: entry.namedFolder, NamePending: entry.namePending}
}

func cacheTorrentMetadata(t *torrent.Torrent) ([]byte, error) {
	if t.Info() == nil {
		return nil, nil
	}
	mi := t.Metainfo()
	var data bytes.Buffer
	if err := mi.Write(&data); err != nil {
		return nil, err
	}
	return data.Bytes(), nil
}

func (e *Engine) restoreSession() {
	if e.session == nil {
		return
	}
	for _, record := range orderedSession(e.session.records) {
		spec, err := e.sessionSpec(record)
		if err == nil {
			_, err = e.addTorrentSpec(spec, record, true)
		}
		if err != nil {
			e.mu.Lock()
			e.recoveryError += fmt.Sprintf("Cannot restore %s: %v\n", record.InfoHash, err)
			e.mu.Unlock()
		}
	}
}

// Restoration never needs an HTTP fetch when cached metainfo is available.
func (e *Engine) sessionSpec(record sessionTorrent) (*torrent.TorrentSpec, error) {
	if len(record.Metainfo) != 0 {
		// Session input is bounded separately. Metadata learned from magnets may
		// exceed the 4 MB limit used for importing external .torrent files.
		mi, err := metainfo.Load(bytes.NewReader(record.Metainfo))
		if err != nil {
			return nil, fmt.Errorf("cached metadata: %w", err)
		}
		spec, err := torrent.TorrentSpecFromMetaInfoErr(mi)
		if err != nil {
			return nil, fmt.Errorf("cached metadata: %w", err)
		}
		if torrentSpecID(spec) != record.InfoHash {
			return nil, fmt.Errorf("cached metadata infohash mismatch")
		}
		// Cached metainfo cannot contain the direct peer/source hints of a magnet.
		// Retain these original discovery hints when restoring resolved magnets.
		if original, err := sessionMagnetSpec(record.Source); err == nil {
			if torrentSpecID(original) != record.InfoHash {
				return nil, fmt.Errorf("source infohash mismatch")
			}
			spec.PeerAddrs = original.PeerAddrs
			spec.Sources = original.Sources
			spec.Trackers = append(spec.Trackers, original.Trackers...)
		}
		return spec, nil
	}
	// An unresolved magnet retains its original trackers and peer hints.
	spec, err := sessionMagnetSpec(record.Source)
	if err != nil && filepath.IsAbs(record.Source) {
		spec, err = loadTorrentFileSpec(record.Source)
	}
	if err != nil {
		spec, err = torrent.TorrentSpecFromMagnetUri("magnet:?xt=urn:btih:" + record.InfoHash)
	}
	if err != nil {
		return nil, err
	}
	if torrentSpecID(spec) != record.InfoHash {
		return nil, fmt.Errorf("source infohash mismatch")
	}
	return spec, nil
}

func sessionMagnetSpec(source string) (*torrent.TorrentSpec, error) {
	source = strings.TrimSpace(source)
	if hexInfohash.MatchString(source) || base32Infohash.MatchString(source) {
		source = "magnet:?xt=urn:btih:" + source
	}
	if len(source) >= 7 && strings.EqualFold(source[:7], "magnet:") {
		source = "magnet:" + source[7:]
	}
	return torrent.TorrentSpecFromMagnetUri(source)
}

// Recovery warnings use the existing error banner; no new UI controls are needed.
func (e *Engine) GetRecoveryError() string {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return strings.TrimSpace(e.recoveryError)
}
