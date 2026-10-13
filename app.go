package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"

	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/runtime"
	"torrent-multipath/pkg/engine"
	"torrent-multipath/pkg/network"
	"torrent-multipath/pkg/state"
)

// App manages the desktop application lifecycle and exposes methods to the frontend.
type App struct {
	ctx           context.Context
	engine        *engine.Engine
	emitter       *state.Emitter
	mu            sync.RWMutex
	initialMagnet string
	startupError  string
	magnetInbox   magnetInbox
}

// NewApp creates a new App application struct.
func NewApp() *App {
	return &App{}
}

// Startup is called at application launch by Wails.
func (a *App) Startup(ctx context.Context) {
	a.magnetInbox.add(a.initialMagnet)
	a.mu.Lock()
	a.ctx = ctx
	a.mu.Unlock()
	// Refresh the current user's handler on every launch, including after moving the EXE.
	if magnetProtocolSupported {
		executable, err := os.Executable()
		if err == nil {
			err = registerMagnetProtocol(executable)
		}
		if err != nil {
			runtime.LogWarningf(ctx, "Magnet registration: %v", err)
		}
	}

	eng, err := engine.NewEngine("")
	if err != nil {
		a.magnetInbox.close()
		a.mu.Lock()
		a.startupError = fmt.Sprintf("Cannot initialize torrent engine: %v", err)
		a.mu.Unlock()
		runtime.LogErrorf(ctx, "Failed initializing Bondo engine: %v", err)
		return
	}
	a.mu.Lock()
	a.engine = eng
	a.mu.Unlock()
	a.magnetInbox.start(a.addExternalMagnet)

	a.emitter = state.NewEmitter(a)
	a.emitter.Start(ctx)

	runtime.LogInfo(ctx, "Bondo engine started successfully with multi-adapter bonding")
}

// Shutdown is called during application teardown.
func (a *App) Shutdown(ctx context.Context) {
	a.magnetInbox.close()
	if a.emitter != nil {
		a.emitter.Stop()
	}
	a.mu.Lock()
	eng := a.engine
	a.engine = nil
	a.mu.Unlock()
	if eng != nil {
		eng.Close()
	}
}

func (a *App) onSecondInstanceLaunch(data options.SecondInstanceData) {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx != nil {
		runtime.WindowShow(ctx)
		runtime.WindowUnminimise(ctx)
	}
	magnet, err := magnetFromArgs(data.Args)
	if err != nil {
		a.mu.Lock()
		a.startupError = fmt.Sprintf("Cannot open magnet link: %v", err)
		a.mu.Unlock()
		return
	}
	a.magnetInbox.add(magnet)
}

func (a *App) addExternalMagnet(magnet string) {
	_, err := a.AddMagnet(magnet)
	a.mu.Lock()
	defer a.mu.Unlock()
	if err != nil {
		a.startupError = fmt.Sprintf("Cannot open magnet link: %v", err)
	} else {
		a.startupError = ""
	}
}

// AddMagnet parses and starts downloading a BitTorrent magnet link.
func (a *App) AddMagnet(url string) (string, error) {
	eng := a.currentEngine()
	if eng == nil {
		return "", fmt.Errorf("engine not initialized")
	}
	return eng.AddMagnet(url)
}

// AddTorrentFile lets the user choose local metainfo and starts its download.
// Cancelling the native picker returns an empty ID without adding a transfer.
func (a *App) AddTorrentFile() (string, error) {
	a.mu.RLock()
	ctx := a.ctx
	a.mu.RUnlock()
	if ctx == nil {
		return "", fmt.Errorf("runtime context not ready")
	}
	if a.currentEngine() == nil {
		return "", fmt.Errorf("engine not initialized")
	}
	path, err := runtime.OpenFileDialog(ctx, runtime.OpenDialogOptions{
		Title:   "Add torrent file",
		Filters: []runtime.FileFilter{{DisplayName: "Torrent files (*.torrent)", Pattern: "*.torrent"}},
	})
	if err != nil || path == "" {
		return "", err
	}
	eng := a.currentEngine()
	if eng == nil {
		return "", fmt.Errorf("engine not initialized")
	}
	return eng.AddTorrentFile(path)
}

// GetAdapters returns the list of detected physical network adapters and their live stats.
func (a *App) GetAdapters() []state.AdapterStats {
	eng := a.currentEngine()
	if eng == nil {
		return []state.AdapterStats{}
	}
	return eng.GetAdapters()
}

// RescanAdapters detects newly connected network interfaces (e.g., USB tethering or Wi-Fi).
func (a *App) RescanAdapters() ([]state.AdapterStats, error) {
	eng := a.currentEngine()
	if eng == nil {
		return []state.AdapterStats{}, fmt.Errorf("engine not initialized")
	}
	return eng.RescanAdapters()
}

// ToggleAdapter enables or disables an adapter from the multipath bonding pool.
func (a *App) ToggleAdapter(id string, enabled bool) error {
	eng := a.currentEngine()
	if eng == nil {
		return fmt.Errorf("engine not initialized")
	}
	eng.ToggleAdapter(id, enabled)
	return nil
}

// GetTorrents returns all active transfers.
func (a *App) GetTorrents() []state.TorrentInfo {
	eng := a.currentEngine()
	if eng == nil {
		return []state.TorrentInfo{}
	}
	return eng.GetTorrents()
}

// PauseTorrent suspends downloading for the specified torrent.
func (a *App) PauseTorrent(id string) error {
	eng := a.currentEngine()
	if eng == nil {
		return fmt.Errorf("engine not initialized")
	}
	return eng.PauseTorrent(id)
}

// ResumeTorrent resumes downloading for the specified torrent.
func (a *App) ResumeTorrent(id string) error {
	eng := a.currentEngine()
	if eng == nil {
		return fmt.Errorf("engine not initialized")
	}
	return eng.ResumeTorrent(id)
}

// RemoveTorrent removes a torrent from the client, optionally deleting files.
func (a *App) RemoveTorrent(id string, deleteData bool) error {
	eng := a.currentEngine()
	if eng == nil {
		return fmt.Errorf("engine not initialized")
	}
	return eng.RemoveTorrent(id, deleteData)
}

// GetPieceStates returns the bitfield array for the piece matrix visualizer.
func (a *App) GetPieceStates(id string) []int {
	eng := a.currentEngine()
	if eng == nil {
		return []int{}
	}
	return eng.GetPieceStates(id)
}

// GetPeerDiagnostics returns peer-to-NIC routing table mappings.
func (a *App) GetPeerDiagnostics() []state.PeerDiagnostic {
	eng := a.currentEngine()
	if eng == nil {
		return []state.PeerDiagnostic{}
	}
	return eng.GetPeerDiagnostics()
}

// CalculateBondEfficiency satisfies the DataProvider interface.
func (a *App) CalculateBondEfficiency(adapters []state.AdapterStats) float64 {
	return network.CalculateBondEfficiency(adapters)
}

func (a *App) currentEngine() *engine.Engine {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.engine
}

func (a *App) GetStats() state.DownloadStats {
	return state.BuildSnapshot(a)
}

// GetError keeps startup and external-link errors in every live snapshot.
func (a *App) GetError() string {
	a.mu.RLock()
	startupError, eng := a.startupError, a.engine
	a.mu.RUnlock()
	if eng != nil {
		return strings.TrimSpace(startupError + "\n" + eng.GetRecoveryError())
	}
	return startupError
}
