package state

// AdapterType classifies physical network interfaces.
type AdapterType string

const (
	AdapterTypeWiFi     AdapterType = "wifi"
	AdapterTypeEthernet AdapterType = "ethernet"
	AdapterTypeCellular AdapterType = "cellular"
	AdapterTypeOther    AdapterType = "other"
)

// AdapterStats represents real-time performance and identity of an active network adapter.
type AdapterStats struct {
	ID                string      `json:"id"`
	Name              string      `json:"name"`
	InterfaceName     string      `json:"interfaceName"`
	IP                string      `json:"ip"`
	HardwareAddr      string      `json:"hardwareAddr"`
	Type              AdapterType `json:"type"`
	Active            bool        `json:"active"`
	Enabled           bool        `json:"enabled"`
	DownloadSpeed     int64       `json:"downloadSpeed"`     // bytes per second
	UploadSpeed       int64       `json:"uploadSpeed"`       // bytes per second
	TotalDownloaded   int64       `json:"totalDownloaded"`   // lifetime bytes
	TotalUploaded     int64       `json:"totalUploaded"`     // lifetime bytes
	ActiveConnections int         `json:"activeConnections"` // current peer sockets
	TotalConnections  int64       `json:"totalConnections"`  // total sockets dialed
	Color             string      `json:"color"`             // UI accent color
	SpeedHistory      []int64     `json:"speedHistory"`      // historical samples for sparklines
	PingMs            int64       `json:"pingMs"`            // live latency in ms
}

// TorrentFile describes an individual file contained within a torrent.
type TorrentFile struct {
	Path           string  `json:"path"`
	Length         int64   `json:"length"`
	BytesCompleted int64   `json:"bytesCompleted"`
	Progress       float64 `json:"progress"`
}

// TorrentStatus represents the lifecycle phase of a torrent.
type TorrentStatus string

const (
	StatusDownloading TorrentStatus = "downloading"
	StatusSeeding     TorrentStatus = "seeding"
	StatusPaused      TorrentStatus = "paused"
	StatusMetadata    TorrentStatus = "metadata"
	StatusChecking    TorrentStatus = "checking"
	StatusError       TorrentStatus = "error"
)

// TorrentInfo represents real-time download status, speeds, and file details.
type TorrentInfo struct {
	ID              string        `json:"id"`
	Name            string        `json:"name"`
	Error           string        `json:"error"`
	Status          TorrentStatus `json:"status"`
	TotalBytes      int64         `json:"totalBytes"`
	DownloadedBytes int64         `json:"downloadedBytes"`
	UploadedBytes   int64         `json:"uploadedBytes"`
	Progress        float64       `json:"progress"` // 0.0 to 100.0
	DownloadSpeed   int64         `json:"downloadSpeed"`
	UploadSpeed     int64         `json:"uploadSpeed"`
	ConnectedPeers  int           `json:"connectedPeers"`
	TotalPeers      int           `json:"totalPeers"`
	ETASeconds      int64         `json:"etaSeconds"`
	Ratio           float64       `json:"ratio"`
	PieceLength     int64         `json:"pieceLength"`
	TotalPieces     int           `json:"totalPieces"`
	CompletedPieces int           `json:"completedPieces"`
	Files           []TorrentFile `json:"files"`
	AddedAt         int64         `json:"addedAt"` // unix timestamp
	DownloadDir     string        `json:"downloadDir"`
}

// PeerDiagnostic tracks an active peer TCP connection mapped to its local egress adapter.
type PeerDiagnostic struct {
	ID              string `json:"id"`
	PeerIP          string `json:"peerIp"`
	AdapterID       string `json:"adapterId"`
	AdapterName     string `json:"adapterName"`
	AdapterIP       string `json:"adapterIp"`
	ConnectedAt     int64  `json:"connectedAt"` // unix timestamp
	BytesDownloaded int64  `json:"bytesDownloaded"`
	BytesUploaded   int64  `json:"bytesUploaded"`
	Client          string `json:"client"`
}

// DownloadStats represents the aggregated system telemetry emitted to the frontend every 500ms.
type DownloadStats struct {
	Error              string           `json:"error"`
	TotalDownloadSpeed int64            `json:"totalDownloadSpeed"`
	TotalUploadSpeed   int64            `json:"totalUploadSpeed"`
	TotalDownloaded    int64            `json:"totalDownloaded"`
	TotalUploaded      int64            `json:"totalUploaded"`
	BondEfficiency     float64          `json:"bondEfficiency"` // 0-100% multipath balance score
	ActiveAdapterCount int              `json:"activeAdapterCount"`
	Adapters           []AdapterStats   `json:"adapters"`
	Torrents           []TorrentInfo    `json:"torrents"`
	PeerDiagnostics    []PeerDiagnostic `json:"peerDiagnostics"`
}
