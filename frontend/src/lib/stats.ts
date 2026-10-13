import type { DownloadStats } from '../types';

export const emptyStats: DownloadStats = {
  totalDownloadSpeed: 0, totalUploadSpeed: 0, totalDownloaded: 0, totalUploaded: 0,
  bondEfficiency: 0, activeAdapterCount: 0, adapters: [], torrents: [], peerDiagnostics: [],
};

const positive = (n: number | undefined) => Number.isFinite(n) ? Math.max(0, n!) : 0;

export function normalizeStats(data?: Partial<DownloadStats> | null): DownloadStats {
  return {
    ...emptyStats, ...data,
    totalDownloadSpeed: positive(data?.totalDownloadSpeed), totalUploadSpeed: positive(data?.totalUploadSpeed),
    totalDownloaded: positive(data?.totalDownloaded), totalUploaded: positive(data?.totalUploaded),
    bondEfficiency: Math.min(100, positive(data?.bondEfficiency)), activeAdapterCount: positive(data?.activeAdapterCount),
    adapters: (Array.isArray(data?.adapters) ? data.adapters : []).map(a => ({ ...a, speedHistory: a.speedHistory ?? [] })),
    torrents: (Array.isArray(data?.torrents) ? data.torrents : []).map(t => ({ ...t, files: t.files ?? [], progress: Math.min(100, positive(t.progress)) })),
    peerDiagnostics: Array.isArray(data?.peerDiagnostics) ? data.peerDiagnostics : [],
  };
}

export function errorMessage(error: unknown): string {
  if (error instanceof Error) return error.message;
  if (typeof error === 'string') return error;
  return 'The action failed. Please try again.';
}
