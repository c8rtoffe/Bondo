package engine

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
)

func validTorrentFolderLeaf(name string) bool {
	if name == "" || name == "." || name == ".." || filepath.IsAbs(name) ||
		filepath.Base(name) != name || strings.TrimRight(name, " .") != name {
		return false
	}
	for _, r := range name {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return false
		}
	}
	stem := strings.ToUpper(strings.TrimSpace(strings.SplitN(name, ".", 2)[0]))
	if stem == "CON" || stem == "PRN" || stem == "AUX" || stem == "NUL" {
		return false
	}
	if len([]rune(stem)) == 4 && (strings.HasPrefix(stem, "COM") || strings.HasPrefix(stem, "LPT")) {
		if strings.ContainsRune("123456789¹²³", []rune(stem)[3]) {
			return false
		}
	}
	return true
}

func torrentFolderName(name string) string {
	name = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) || strings.ContainsRune(`<>:"/\|?*`, r) {
			return '_'
		}
		return r
	}, name)
	name = strings.Trim(name, " .")
	runes := []rune(name)
	if len(runes) > 100 {
		name = strings.TrimRight(string(runes[:100]), " .")
	}
	if name == "" {
		return "Torrent"
	}
	if !validTorrentFolderLeaf(name) {
		name = "_" + name
	}
	return name
}

// All transfer folders are direct children of the recorded canonical root.
func transferDirectory(base, leaf string) (string, error) {
	if !filepath.IsAbs(base) || !validTorrentFolderLeaf(leaf) {
		return "", fmt.Errorf("invalid transfer folder")
	}
	resolvedBase, err := filepath.EvalSymlinks(base)
	if err != nil {
		return "", err
	}
	if !strings.EqualFold(filepath.Clean(base), filepath.Clean(resolvedBase)) {
		return "", fmt.Errorf("download root has changed")
	}
	target := filepath.Join(base, leaf)
	if info, err := os.Lstat(target); err == nil {
		if info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
			return "", fmt.Errorf("torrent folder must be a regular directory")
		}
		resolved, err := filepath.EvalSymlinks(target)
		if err != nil {
			return "", err
		}
		rel, err := filepath.Rel(base, resolved)
		if err != nil || !strings.EqualFold(rel, leaf) {
			return "", fmt.Errorf("torrent folder is outside its download root")
		}
	} else if !os.IsNotExist(err) {
		return "", err
	}
	return target, nil
}

func ownershipFile(dir, id string) string { return filepath.Join(dir, ".bondo-torrent-"+id) }

func ownsTorrentFolder(dir, id string) bool {
	owned, _ := readFolderOwnership(dir, id)
	return owned
}

func hasOtherTorrentOwner(dir, id string) (bool, error) {
	children, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	for _, child := range children {
		other := strings.TrimPrefix(child.Name(), ".bondo-torrent-")
		if other != id && torrentIDPattern.MatchString(other) && ownsTorrentFolder(dir, other) {
			return true, nil
		}
	}
	return false, nil
}

// A marker makes re-adding the same torrent reuse its files, while folders owned
// by another torrent or by the user always receive a separate numeric suffix.
func (s *StorageManager) createNamedTorrentDir(base, id, name string) (string, bool, error) {
	if !torrentIDPattern.MatchString(id) {
		return "", false, fmt.Errorf("invalid torrent ID")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(base, 0755); err != nil {
		return "", false, err
	}
	legacy, err := transferDirectory(base, id)
	if err != nil {
		return "", false, err
	}
	if _, err := os.Stat(legacy); err == nil {
		other, err := hasOtherTorrentOwner(legacy, id)
		if err != nil {
			return "", false, err
		}
		if !other {
			return legacy, ownsTorrentFolder(legacy, id), nil
		}
	}
	name = torrentFolderName(name)
	// Search existing ownership before using any free name. A lower-numbered
	// folder may have been removed since this torrent first used "Movie (2)".
	children, err := os.ReadDir(base)
	if err != nil {
		return "", false, err
	}
	for _, child := range children {
		if !child.IsDir() {
			continue
		}
		dir, err := transferDirectory(base, child.Name())
		if err == nil && ownsTorrentFolder(dir, id) {
			return dir, true, nil
		}
	}
	for index := 1; index <= 10000; index++ {
		leaf := name
		if index > 1 {
			leaf = fmt.Sprintf("%s (%d)", name, index)
		}
		dir, err := transferDirectory(base, leaf)
		if err != nil {
			// An occupied file/symlink is a collision, never a location to overwrite.
			if _, statErr := os.Lstat(filepath.Join(base, leaf)); statErr == nil {
				continue
			}
			return "", false, err
		}
		if ownsTorrentFolder(dir, id) {
			return dir, true, nil
		}
		err = os.Mkdir(dir, 0755)
		if os.IsExist(err) {
			continue
		}
		if err != nil {
			return "", false, err
		}
		if err := writeSessionAtomic(ownershipFile(dir, id), ownershipContents(id, true)); err != nil {
			os.Remove(dir) // Only remove the newly created directory if still empty.
			return "", false, err
		}
		return dir, true, nil
	}
	return "", false, fmt.Errorf("too many folders with the same torrent name")
}

func prepareRecordedTorrentDir(base, dir, id string, named bool) error {
	if !torrentIDPattern.MatchString(id) || !filepath.IsAbs(dir) ||
		filepath.Clean(filepath.Dir(dir)) != filepath.Clean(base) {
		return fmt.Errorf("invalid recorded transfer folder")
	}
	if err := os.MkdirAll(base, 0755); err != nil {
		return err
	}
	target, err := transferDirectory(base, filepath.Base(dir))
	if err != nil {
		return err
	}
	if !named && filepath.Base(dir) != id {
		return fmt.Errorf("invalid legacy folder")
	}
	if !named {
		other, err := hasOtherTorrentOwner(target, id)
		if err != nil {
			return err
		}
		if other {
			return fmt.Errorf("legacy folder belongs to another torrent")
		}
		return os.MkdirAll(target, 0755)
	}
	err = os.Mkdir(target, 0755)
	if os.IsExist(err) {
		if !ownsTorrentFolder(target, id) {
			return fmt.Errorf("recorded torrent folder belongs to another transfer")
		}
		return nil
	}
	if err != nil {
		return err
	}
	if err := writeSessionAtomic(ownershipFile(target, id), ownershipContents(id, true)); err != nil {
		os.Remove(target)
		return err
	}
	return nil
}

// Select the real name inside OpenTorrent, before mmap creates payload files.
// Never move files that have already been mapped, which is unsafe on Windows.
type transferStorage struct {
	mu                     sync.Mutex
	manager                *StorageManager
	base, dir, id          string
	named, pending, closed bool
	inner                  storage.ClientImplCloser
}

func (s *transferStorage) OpenTorrent(ctx context.Context, info *metainfo.Info, hash metainfo.Hash) (storage.TorrentImpl, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return storage.TorrentImpl{}, os.ErrClosed
	}
	if s.inner == nil {
		if s.pending {
			dir, named, err := s.manager.createNamedTorrentDir(s.base, s.id, info.BestName())
			if err != nil {
				return storage.TorrentImpl{}, err
			}
			s.dir, s.named, s.pending = dir, named, false
		}
		s.inner = storage.NewMMapWithCompletion(s.dir, storage.NewMapPieceCompletion())
	}
	storageInfo := info
	if s.named && info.IsDir() {
		if err := prepareFlatTorrentDirectory(s.dir, s.id, info); err != nil {
			return storage.TorrentImpl{}, err
		}
		// Change only storage's private copy. Torrent metadata, hashes and file
		// offsets remain identical, including UTF-8 names and v2 file trees.
		copyInfo := *info
		copyInfo.Name, copyInfo.NameUtf8 = ".", ""
		storageInfo = &copyInfo
	}
	return s.inner.OpenTorrent(ctx, storageInfo, hash)
}

func (s *transferStorage) location() (base, dir string, named, pending bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.base, s.dir, s.named, s.pending
}

func (s *transferStorage) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	if s.inner != nil {
		return s.inner.Close()
	}
	return nil
}

// Each transfer supplies its own storage. A stateless fallback prevents the
// torrent client from initializing an unused on-disk completion database.
type requiredTransferStorage struct{}

func (requiredTransferStorage) OpenTorrent(context.Context, *metainfo.Info, metainfo.Hash) (storage.TorrentImpl, error) {
	return storage.TorrentImpl{}, fmt.Errorf("torrent requires its own transfer storage")
}

const flatFolderLayout = "flat-v1\n"

func ownershipContents(id string, flat bool) []byte {
	value := id + "\n"
	if flat {
		value += flatFolderLayout
	}
	return []byte(value)
}

// Older markers contain only the hash. Keeping the layout in the same marker
// also makes re-adds and interrupted migrations independent of session writes.
func readFolderOwnership(dir, id string) (owned, flat bool) {
	path := ownershipFile(dir, id)
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 64 {
		return false, false
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, false
	}
	value := string(data)
	return value == id+"\n" || value == id+"\n"+flatFolderLayout,
		value == id+"\n"+flatFolderLayout
}

// A named transfer folder already supplies the torrent's root directory.
// Move old nested contents before mapping any files; each same-volume rename
// preserves partial data, and a restart continues any interrupted migration.
func prepareFlatTorrentDirectory(dir, id string, info *metainfo.Info) error {
	owned, flat := readFolderOwnership(dir, id)
	if !owned {
		return fmt.Errorf("torrent folder ownership is missing")
	}
	// Never let a payload overwrite ownership metadata after flattening.
	for _, file := range info.UpvertedFiles() {
		path, err := storage.ToSafeFilePath(file.BestPath()...)
		if err != nil || !filepath.IsLocal(path) || path == "." {
			return fmt.Errorf("invalid torrent file path")
		}
		top := strings.SplitN(path, string(filepath.Separator), 2)[0]
		if strings.HasPrefix(strings.ToLower(top), ".bondo-torrent-") {
			return fmt.Errorf("torrent file conflicts with folder ownership")
		}
	}
	if flat {
		return nil
	}
	name := info.BestName()
	if !validTorrentFolderLeaf(name) {
		return fmt.Errorf("cannot migrate invalid legacy torrent folder name")
	}
	nested, err := transferDirectory(dir, name)
	if err != nil {
		return err
	}
	children, err := os.ReadDir(nested)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	// Validate every destination before moving anything. Existing user files
	// and links are preserved; collisions surface as a recovery error.
	for _, child := range children {
		from := filepath.Join(nested, child.Name())
		to := filepath.Join(dir, child.Name())
		resolved, err := filepath.EvalSymlinks(from)
		if err != nil || !strings.EqualFold(filepath.Clean(from), filepath.Clean(resolved)) {
			return fmt.Errorf("cannot migrate linked torrent content %q", child.Name())
		}
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("cannot migrate torrent: destination %q already exists", child.Name())
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	for _, child := range children {
		from := filepath.Join(nested, child.Name())
		to := filepath.Join(dir, child.Name())
		// Recheck because a user may create a file while the app is starting.
		if _, err := os.Lstat(to); err == nil {
			return fmt.Errorf("cannot migrate torrent: destination %q already exists", child.Name())
		} else if !os.IsNotExist(err) {
			return err
		}
		if err := os.Rename(from, to); err != nil {
			return fmt.Errorf("migrate torrent file: %w", err)
		}
	}
	if err := os.Remove(nested); err != nil && !os.IsNotExist(err) {
		return err // Only remove the obsolete directory if it is empty.
	}
	return writeSessionAtomic(ownershipFile(dir, id), ownershipContents(id, true))
}
