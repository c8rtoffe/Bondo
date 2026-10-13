package engine

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/anacrolix/torrent"
	"torrent-multipath/pkg/network"
	"torrent-multipath/pkg/state"
)

type torrentEntry struct {
	t            *torrent.Torrent
	addedAt      int64
	paused       bool
	lastBytes    int64
	lastSampleAt time.Time
	currentSpeed int64
	lastUploaded int64
	uploadSpeed  int64
	baseDir      string
	downloadDir  string
	fileStorage  *transferStorage
	lastError    string
	source       string
	namedFolder  bool
	namePending  bool
	metainfo     []byte
	checking     bool
}

// Engine coordinates the multi-homed BitTorrent client, dialer pool, and network monitors.
type Engine struct {
	mu            sync.RWMutex
	client        *torrent.Client
	dialerPool    *DialerPool
	detector      *network.Detector
	monitor       *network.Monitor
	storage       *StorageManager
	torrents      map[string]*torrentEntry
	closed        bool
	session       *sessionStore
	recoveryError string
	workers       sync.WaitGroup
	ctx           context.Context
	cancel        context.CancelFunc
}

// NewEngine initializes the core torrent subsystem with multipath socket routing.
func NewEngine(customDownloadDir string) (*Engine, error) {
	sessionPath, err := defaultSessionPath()
	if err != nil {
		return nil, fmt.Errorf("session directory: %w", err)
	}
	session, err := openSessionStore(sessionPath)
	if err != nil {
		return nil, err
	}
	storage, err := NewStorageManager(customDownloadDir)
	if err != nil {
		return nil, fmt.Errorf("storage initialization failed: %w", err)
	}

	detector := network.NewDetector()
	adapters, err := detector.Scan()
	if err != nil {
		return nil, fmt.Errorf("failed scanning network adapters: %w", err)
	}

	monitor := network.NewMonitor()
	dialerPool := NewDialerPool(adapters, monitor)

	cfg := engineClientConfig(storage.GetBaseDir())
	client, err := torrent.NewClient(cfg)
	if err != nil {
		monitor.Stop()
		return nil, fmt.Errorf("failed to initialize torrent client: %w", err)
	}

	// Register the custom round-robin multipath dialer
	client.AddDialer(dialerPool)

	ctx, cancel := context.WithCancel(context.Background())
	engine := &Engine{
		ctx: ctx, cancel: cancel,
		client:     client,
		dialerPool: dialerPool,
		detector:   detector,
		monitor:    monitor,
		storage:    storage,
		torrents:   make(map[string]*torrentEntry),
		session:    session,
	}

	engine.restoreSession()
	return engine, nil
}

// Centralized so local swarm tests exercise the same peer-routing settings as the app.
func engineClientConfig(dataDir string) *torrent.ClientConfig {
	cfg := torrent.NewDefaultClientConfig()
	cfg.DataDir = dataDir
	cfg.DefaultStorage = requiredTransferStorage{}
	cfg.DisableWebseeds = true
	cfg.DisableWebtorrent = true
	cfg.Seed = true
	cfg.DisableTCP = true // Prevent the built-in unbound TCP dialer from bypassing adapter selection.
	cfg.NoDefaultPortForwarding = true
	cfg.ListenPort = 0
	cfg.DisableIPv6 = true
	cfg.DisableUTP = true       // Direct peer traffic over TCP to utilize interface-bound sockets
	cfg.DialForPeerConns = true // Use dialers for peer connections
	cfg.EstablishedConnsPerTorrent = 80
	cfg.HalfOpenConnsPerTorrent = 40
	cfg.NominalDialTimeout = 10 * time.Second

	return cfg
}

// AddMagnet accepts a magnet URI, infohash or torrent URL and initiates download.
func (e *Engine) AddMagnet(uri string) (string, error) {
	spec, err := loadTorrentSpec(e.ctx, uri, &http.Client{Timeout: 15 * time.Second})
	if err != nil {
		return "", err
	}
	source := strings.TrimSpace(uri)
	if hexInfohash.MatchString(source) || base32Infohash.MatchString(source) {
		source = "magnet:?xt=urn:btih:" + source
	}
	return e.addTorrentSpec(spec, sessionTorrent{Source: source, AddedAt: time.Now().Unix()}, false)
}

// AddTorrentFile starts a local torrent file through the same storage and routing path.
func (e *Engine) AddTorrentFile(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	spec, err := loadTorrentFileSpec(path)
	if err != nil {
		return "", err
	}
	return e.addTorrentSpec(spec, sessionTorrent{Source: path, AddedAt: time.Now().Unix()}, false)
}

func (e *Engine) addTorrentSpec(spec *torrent.TorrentSpec, record sessionTorrent, restoring bool) (string, error) {
	id := torrentSpecID(spec)
	if restoring && id != record.InfoHash {
		return "", fmt.Errorf("session infohash mismatch")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return "", fmt.Errorf("torrent engine is closed")
	}
	// Webseed HTTP traffic would bypass the adapter-bound peer dialer.
	spec.Webseeds = nil
	if entry, exists := e.torrents[id]; exists {
		if err := entry.t.MergeSpec(spec); err != nil {
			return "", err
		}
		metadata, err := cacheTorrentMetadata(entry.t)
		if err != nil {
			return "", err
		}
		entry.metainfo = metadata
		if err := e.saveEntryLocked(id, entry); err != nil {
			return "", err
		}
		return id, nil
	}
	base := e.storage.GetBaseDir()
	dir := filepath.Join(base, id)
	pending, named := true, true
	if restoring {
		base, dir = filepath.Dir(record.DownloadDir), record.DownloadDir
		pending, named = record.NamePending, record.NamedFolder
		if !pending {
			if err := prepareRecordedTorrentDir(base, dir, id, named); err != nil {
				return "", err
			}
		}
	}
	fileStorage := &transferStorage{manager: e.storage, base: base, dir: dir, id: id, named: named, pending: pending}
	spec.Storage = fileStorage
	spec.DisallowDataDownload = true
	spec.DisallowDataUpload = true
	t, _, err := e.client.AddTorrentSpec(spec)
	if err != nil {
		fileStorage.Close()
		return "", fmt.Errorf("failed adding torrent: %w", err)
	}
	entry := &torrentEntry{t: t, addedAt: record.AddedAt, paused: record.Paused,
		source: record.Source, lastSampleAt: time.Now(), baseDir: base,
		downloadDir: dir, fileStorage: fileStorage, checking: restoring}
	entry.baseDir, entry.downloadDir, entry.namedFolder, entry.namePending = fileStorage.location()
	entry.metainfo, err = cacheTorrentMetadata(t)
	if err == nil && !restoring {
		err = e.saveEntryLocked(id, entry)
	}
	if err != nil {
		t.Drop()
		fileStorage.Close()
		return "", fmt.Errorf("cannot record torrent session: %w", err)
	}
	e.torrents[id] = entry
	t.SetOnWriteChunkError(func(err error) {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.torrents[id] == entry {
			entry.lastError = fmt.Sprintf("Cannot write downloaded data: %v", err)
			entry.t.DisallowDataDownload()
		}
	})
	e.workers.Add(1)
	go e.startTransfer(id, entry, restoring)
	return id, nil
}

// startTransfer waits for metadata and, on recovery, verifies every piece before
// allowing payload traffic. No engine lock is held during disk hashing.
func (e *Engine) startTransfer(id string, entry *torrentEntry, restoring bool) {
	defer e.workers.Done()
	t := entry.t
	select {
	case <-e.ctx.Done():
		return
	case <-t.Closed():
		return
	case <-t.GotInfo():
	}
	e.mu.Lock()
	if e.closed || e.torrents[id] != entry {
		e.mu.Unlock()
		return
	}
	entry.baseDir, entry.downloadDir, entry.namedFolder, entry.namePending = entry.fileStorage.location()
	metadata, err := cacheTorrentMetadata(t)
	if err == nil {
		entry.metainfo = metadata
		err = e.saveEntryLocked(id, entry)
	}
	if err != nil {
		entry.lastError = fmt.Sprintf("Cannot save torrent session: %v", err)
	}
	e.mu.Unlock()
	if restoring {
		// A new map completion store marks every piece unknown, so the client queues
		// initial disk hashes as metadata is installed. Wait for those hashes rather
		// than forcing a second pass over the same files.
		err = waitForInitialChecks(e.ctx, t)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || e.torrents[id] != entry {
		return
	}
	entry.checking = false
	if restoring && err != nil {
		entry.lastError = fmt.Sprintf("Cannot verify existing files: %v", err)
		return
	}
	if !entry.paused && entry.lastError == "" {
		t.AllowDataDownload()
		t.AllowDataUpload()
		t.DownloadAll()
		PrioritizeInitialPieces(t, 5)
	}
}

func waitForInitialChecks(ctx context.Context, t *torrent.Torrent) error {
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		checking := false
		for _, run := range t.PieceStateRuns() {
			if run.Checking || run.Hashing || run.QueuedForHash {
				checking = true
				break
			}
		}
		if !checking {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.Closed():
			return fmt.Errorf("torrent closed during verification")
		case <-ticker.C:
		}
	}
}

// PauseTorrent suspends chunk downloads for the given torrent infohash.
func (e *Engine) PauseTorrent(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("torrent engine is closed")
	}

	entry, ok := e.torrents[id]
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	record := entry.sessionRecord(id)
	record.Paused = true
	if e.session != nil {
		if err := e.session.update(id, &record); err != nil {
			return err
		}
	}
	entry.paused = true
	entry.currentSpeed = 0
	entry.uploadSpeed = 0
	entry.t.DisallowDataDownload()
	entry.t.DisallowDataUpload()
	return nil
}

// ResumeTorrent re-queues all incomplete chunks for the given torrent.
func (e *Engine) ResumeTorrent(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("torrent engine is closed")
	}

	entry, ok := e.torrents[id]
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	record := entry.sessionRecord(id)
	record.Paused = false
	if e.session != nil {
		if err := e.session.update(id, &record); err != nil {
			return err
		}
	}
	entry.paused = false
	entry.lastError = ""
	if !entry.checking && !entry.namePending {
		entry.t.AllowDataDownload()
		entry.t.AllowDataUpload()
		if entry.t.Info() != nil {
			entry.t.DownloadAll()
		}
	}
	return nil
}

// RemoveTorrent stops and drops the torrent, optionally deleting downloaded data on disk.
func (e *Engine) RemoveTorrent(id string, deleteData bool) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("torrent engine is closed")
	}

	entry, ok := e.torrents[id]
	if !ok {
		return fmt.Errorf("torrent %s not found", id)
	}

	if e.session != nil {
		if err := e.session.update(id, nil); err != nil {
			return err
		}
	}
	entry.t.Drop()
	delete(e.torrents, id)

	if err := entry.fileStorage.Close(); err != nil {
		return fmt.Errorf("closing torrent storage: %w", err)
	}
	base, dir, _, pending := entry.fileStorage.location()
	if deleteData && !pending {
		if err := e.storage.RemoveTorrentFiles(base, id, dir); err != nil {
			return fmt.Errorf("transfer removed, but deleting files failed: %w", err)
		}
	}

	return nil
}

// GetTorrents compiles live metrics for all registered torrent transfers.
func (e *Engine) GetTorrents() []state.TorrentInfo {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return []state.TorrentInfo{}
	}

	results := make([]state.TorrentInfo, 0, len(e.torrents))
	now := time.Now()

	for id, entry := range e.torrents {
		t := entry.t
		stats := t.Stats()

		var name string = "Fetching metadata..."
		var totalBytes int64 = 0
		var pieceLength int64 = 0
		var totalPieces int = 0
		filesList := make([]state.TorrentFile, 0)

		info := t.Info()
		if info != nil {
			name = t.Name()
			totalBytes = info.TotalLength()
			pieceLength = info.PieceLength
			totalPieces = info.NumPieces()

			for _, f := range t.Files() {
				filesList = append(filesList, state.TorrentFile{
					Path:           f.Path(),
					Length:         f.Length(),
					BytesCompleted: f.BytesCompleted(),
					Progress:       calcProgress(f.BytesCompleted(), f.Length()),
				})
			}
		}

		completedBytes := t.BytesCompleted()

		// Calculate instant download speed
		elapsed := now.Sub(entry.lastSampleAt).Seconds()
		if elapsed >= 0.5 {
			downloaded := stats.BytesReadUsefulData.Int64()
			delta := downloaded - entry.lastBytes
			if delta < 0 {
				delta = 0
			}
			entry.currentSpeed = int64(float64(delta) / elapsed)
			entry.lastBytes = downloaded
			uploaded := stats.BytesWrittenData.Int64()
			entry.uploadSpeed = int64(float64(max(uploaded-entry.lastUploaded, 0)) / elapsed)
			entry.lastUploaded = uploaded
			entry.lastSampleAt = now
		}

		if entry.paused || entry.lastError != "" {
			entry.currentSpeed = 0
			entry.uploadSpeed = 0
		}
		progress := calcProgress(completedBytes, totalBytes)
		completedPieces := CountCompletedPieces(t)

		status := transferStatus(entry, info != nil, completedBytes, totalBytes, completedPieces, totalPieces)

		// Calculate ETA (seconds)
		var eta int64 = 0
		if entry.currentSpeed > 0 && totalBytes > completedBytes {
			remaining := totalBytes - completedBytes
			eta = remaining / entry.currentSpeed
		}

		// Calculate share ratio
		var ratio float64 = 0.0
		if completedBytes > 0 {
			ratio = float64(stats.BytesWrittenData.Int64()) / float64(completedBytes)
		}

		results = append(results, state.TorrentInfo{
			ID:              id,
			Name:            name,
			Status:          status,
			TotalBytes:      totalBytes,
			DownloadedBytes: completedBytes,
			UploadedBytes:   stats.BytesWrittenData.Int64(),
			Progress:        progress,
			DownloadSpeed:   entry.currentSpeed,
			UploadSpeed:     entry.uploadSpeed,
			Error:           entry.lastError,
			ConnectedPeers:  stats.ActivePeers,
			TotalPeers:      stats.TotalPeers,
			ETASeconds:      eta,
			Ratio:           ratio,
			PieceLength:     pieceLength,
			TotalPieces:     totalPieces,
			CompletedPieces: completedPieces,
			Files:           filesList,
			AddedAt:         entry.addedAt,
			DownloadDir:     entry.downloadDir,
		})
	}

	sort.Slice(results, func(i, j int) bool {
		if results[i].AddedAt == results[j].AddedAt {
			return results[i].ID < results[j].ID
		}
		return results[i].AddedAt < results[j].AddedAt
	})
	return results
}

// Received chunks are not complete until all piece hashes have been verified.
func transferStatus(entry *torrentEntry, hasInfo bool, received, total int64, verified, pieces int) state.TorrentStatus {
	switch {
	case entry.lastError != "":
		return state.StatusError
	case entry.paused:
		return state.StatusPaused
	case !hasInfo:
		return state.StatusMetadata
	case entry.checking:
		return state.StatusChecking
	case verified == pieces:
		return state.StatusSeeding
	case received >= total:
		return state.StatusChecking
	default:
		return state.StatusDownloading
	}
}

func calcProgress(completed, total int64) float64 {
	if total <= 0 || completed <= 0 {
		return 0.0
	}
	p := (float64(completed) / float64(total)) * 100.0
	if p > 100.0 {
		return 100.0
	}
	return p
}

// GetPieceStates extracts piece bitfield matrix states for visualization.
func (e *Engine) GetPieceStates(id string) []int {
	e.mu.RLock()
	defer e.mu.RUnlock()

	entry, ok := e.torrents[id]
	if !ok || entry.t.Info() == nil {
		return []int{}
	}

	return ExtractPieceStates(entry.t)
}

// RescanAdapters detects newly connected physical adapters and updates the dialer pool.
func (e *Engine) RescanAdapters() ([]state.AdapterStats, error) {
	adapters, err := e.detector.Scan()
	if err != nil {
		return nil, err
	}
	e.dialerPool.SetAdapters(adapters)
	return e.dialerPool.GetAdapterStats(), nil
}

// ToggleAdapter enables or disables an adapter in the dialer pool.
func (e *Engine) ToggleAdapter(id string, enable bool) {
	e.dialerPool.ToggleAdapter(id, enable)
}

// GetAdapters returns current adapter stats from the pool.
func (e *Engine) GetAdapters() []state.AdapterStats {
	return e.dialerPool.GetAdapterStats()
}

// GetPeerDiagnostics returns peer-to-NIC routing table data.
func (e *Engine) GetPeerDiagnostics() []state.PeerDiagnostic {
	return e.dialerPool.GetPeerDiagnostics()
}

// Close gracefully terminates the engine, connections, and monitors.
func (e *Engine) Close() {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return
	}
	e.closed = true
	e.cancel()
	entries := make([]*torrentEntry, 0, len(e.torrents))
	for _, entry := range e.torrents {
		entries = append(entries, entry)
	}
	e.mu.Unlock()
	e.dialerPool.Close()
	e.client.Close()
	for _, entry := range entries {
		entry.fileStorage.Close()
	}
	e.monitor.Stop()
	e.workers.Wait()
}
