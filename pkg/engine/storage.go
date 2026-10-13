package engine

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
)

var torrentIDPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

// Each transfer owns an isolated folder; legacy hash paths remain supported.
type StorageManager struct {
	mu      sync.RWMutex
	baseDir string
}

func NewStorageManager(customDir string) (*StorageManager, error) {
	if customDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, err
		}
		customDir = filepath.Join(home, "Downloads")
	}
	s := &StorageManager{}
	if err := s.SetBaseDir(customDir); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *StorageManager) GetBaseDir() string { s.mu.RLock(); defer s.mu.RUnlock(); return s.baseDir }

func (s *StorageManager) SetBaseDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return fmt.Errorf("download directory cannot be empty")
	}
	absolute, err := filepath.Abs(dir)
	if err != nil {
		return err
	}
	if err = os.MkdirAll(absolute, 0755); err != nil {
		return fmt.Errorf("cannot create download directory: %w", err)
	}
	absolute, err = filepath.EvalSymlinks(absolute)
	if err != nil {
		return err
	}
	s.mu.Lock()
	s.baseDir = absolute
	s.mu.Unlock()
	return nil
}

func torrentDirectory(base, id string) (string, error) {
	if !torrentIDPattern.MatchString(id) {
		return "", fmt.Errorf("invalid torrent ID")
	}
	target := filepath.Join(base, id)
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("torrent directory must be a regular directory")
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(base, resolved)
		if err != nil || rel != id {
			return "", fmt.Errorf("torrent directory is outside its download root")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func (s *StorageManager) CreateTorrentDir(id string) (string, string, error) {
	base := s.GetBaseDir()
	dir, err := torrentDirectory(base, id)
	if err == nil {
		err = os.MkdirAll(dir, 0755)
	}
	return base, dir, err
}

// Remove only the recorded transfer folder. Named folders require matching ownership.
func (s *StorageManager) RemoveTorrentFiles(base, id string, directories ...string) error {
	if !torrentIDPattern.MatchString(id) || len(directories) > 1 {
		return fmt.Errorf("invalid torrent deletion target")
	}
	dir := filepath.Join(base, id)
	if len(directories) == 1 {
		dir = directories[0]
	}
	if !filepath.IsAbs(dir) || filepath.Clean(filepath.Dir(dir)) != filepath.Clean(base) {
		return fmt.Errorf("torrent folder is outside its download root")
	}
	target, err := transferDirectory(base, filepath.Base(dir))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(target); os.IsNotExist(err) {
		return nil
	}
	other, err := hasOtherTorrentOwner(target, id)
	if err != nil {
		return err
	}
	if other {
		return fmt.Errorf("torrent folder belongs to another transfer")
	}
	if filepath.Base(dir) != id && !ownsTorrentFolder(target, id) {
		return fmt.Errorf("torrent folder ownership does not match")
	}
	return os.RemoveAll(target)
}
