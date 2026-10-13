export type AdapterType = 'wifi' | 'ethernet' | 'cellular' | 'other';

export interface AdapterStats {
  id: string;
  name: string;
  interfaceName?: string;
  ip: string;
  hardwareAddr: string;
  type: AdapterType;
  active: boolean;
  enabled: boolean;
  downloadSpeed: number;
  uploadSpeed: number;
  totalDownloaded: number;
  totalUploaded: number;
  activeConnections: number;
  totalConnections: number;
  color: string;
  speedHistory: number[];
  pingMs?: number;
}

export interface TorrentFile {
  path: string;
  length: number;
  bytesCompleted: number;
  progress: number;
}

export type TorrentStatus = 'downloading' | 'seeding' | 'paused' | 'metadata' | 'checking' | 'error';

export interface TorrentInfo {
  error?: string;
  id: string;
  name: string;
  status: TorrentStatus;
  totalBytes: number;
  downloadedBytes: number;
  uploadedBytes: number;
  progress: number;
  downloadSpeed: number;
  uploadSpeed: number;
  connectedPeers: number;
  totalPeers: number;
  etaSeconds: number;
  ratio: number;
  pieceLength: number;
  totalPieces: number;
  completedPieces: number;
  files: TorrentFile[];
  addedAt: number;
  downloadDir: string;
}

export interface PeerDiagnostic {
  id: string;
  peerIp: string;
  adapterId: string;
  adapterName: string;
  adapterIp: string;
  connectedAt: number;
  bytesDownloaded: number;
  bytesUploaded: number;
  client: string;
}

export interface DownloadStats {
  error?: string;
  totalDownloadSpeed: number;
  totalUploadSpeed: number;
  totalDownloaded: number;
  totalUploaded: number;
  bondEfficiency: number;
  activeAdapterCount: number;
  adapters: AdapterStats[];
  torrents: TorrentInfo[];
  peerDiagnostics: PeerDiagnostic[];
}
