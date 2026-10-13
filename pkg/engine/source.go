package engine

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
)

const maxMetainfoBytes = 4 << 20

var hexInfohash = regexp.MustCompile(`^[a-fA-F0-9]{40}$`)
var base32Infohash = regexp.MustCompile(`^[a-zA-Z2-7]{32}$`)

func loadTorrentSpec(ctx context.Context, input string, client *http.Client) (*torrent.TorrentSpec, error) {
	input = strings.TrimSpace(input)
	if hexInfohash.MatchString(input) || base32Infohash.MatchString(input) {
		input = "magnet:?xt=urn:btih:" + input
	}
	u, err := url.Parse(input)
	if err != nil {
		return nil, fmt.Errorf("invalid download source: %w", err)
	}
	var spec *torrent.TorrentSpec
	switch strings.ToLower(u.Scheme) {
	case "magnet":
		if _, queryErr := url.ParseQuery(u.RawQuery); queryErr != nil {
			return nil, fmt.Errorf("invalid magnet link: %w", queryErr)
		}
		u.Scheme = "magnet"
		spec, err = torrent.TorrentSpecFromMagnetUri(u.String())
	case "http", "https":
		req, reqErr := http.NewRequestWithContext(ctx, http.MethodGet, input, nil)
		if reqErr != nil {
			return nil, reqErr
		}
		response, reqErr := client.Do(req)
		if reqErr != nil {
			return nil, fmt.Errorf("cannot fetch torrent file: %w", reqErr)
		}
		defer response.Body.Close()
		if response.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("torrent server returned HTTP %d", response.StatusCode)
		}
		var loadErr error
		spec, loadErr = readMetainfoSpec(response.Body)
		if loadErr != nil {
			return nil, fmt.Errorf("URL must point to a torrent file: %w", loadErr)
		}
	default:
		return nil, fmt.Errorf("enter a magnet link, a 40-character hex or 32-character base32 infohash, or a torrent file URL")
	}
	if err != nil {
		return nil, fmt.Errorf("invalid torrent: %w", err)
	}
	return validateTorrentSpec(spec)
}

// loadTorrentFileSpec reads bounded local metainfo, never the torrent payload.
func loadTorrentFileSpec(path string) (*torrent.TorrentSpec, error) {
	if !strings.EqualFold(filepath.Ext(path), ".torrent") {
		return nil, fmt.Errorf("select a .torrent file")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("cannot open torrent file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("cannot read torrent file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("select a regular .torrent file")
	}
	if info.Size() > maxMetainfoBytes {
		return nil, fmt.Errorf("torrent file exceeds the 4 MB limit")
	}
	spec, err := readMetainfoSpec(file)
	if err != nil {
		return nil, fmt.Errorf("invalid torrent file: %w", err)
	}
	return validateTorrentSpec(spec)
}

func readMetainfoSpec(reader io.Reader) (*torrent.TorrentSpec, error) {
	data, err := io.ReadAll(io.LimitReader(reader, maxMetainfoBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxMetainfoBytes {
		return nil, fmt.Errorf("torrent file exceeds the 4 MB limit")
	}
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	return torrent.TorrentSpecFromMetaInfoErr(mi)
}

func validateTorrentSpec(spec *torrent.TorrentSpec) (*torrent.TorrentSpec, error) {
	if spec == nil || (spec.InfoHash == ([20]byte{}) && !spec.InfoHashV2.Ok) {
		return nil, fmt.Errorf("torrent has no valid infohash")
	}
	return spec, nil
}
