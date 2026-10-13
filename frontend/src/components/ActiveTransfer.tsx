import React, { useState, useEffect } from 'react';
import { Play, Pause, Trash2, ArrowDown, Users, Clock, FileArchive } from 'lucide-react';
import { TorrentInfo, AdapterStats } from '../types';
import { errorMessage } from '../lib/stats';
import { ChunkMatrix } from './ChunkMatrix';

interface ActiveTransferProps {
  torrent: TorrentInfo;
  adapters: AdapterStats[];
  onPause: (id: string) => Promise<void>;
  onResume: (id: string) => Promise<void>;
  onRemove: (id: string, deleteData: boolean) => Promise<void>;
  onGetPieceStates: (id: string) => Promise<number[]>;
}

const formatSpeed = (b: number) =>
  b >= 1048576 ? `${(b / 1048576).toFixed(1)} MB/s` : b >= 1024 ? `${(b / 1024).toFixed(0)} KB/s` : `${b} B/s`;

const formatBytes = (b: number) =>
  b >= 1073741824
    ? `${(b / 1073741824).toFixed(2)} GB`
    : b >= 1048576
    ? `${(b / 1048576).toFixed(1)} MB`
    : b >= 1024
    ? `${(b / 1024).toFixed(0)} KB`
    : `${b} B`;

const formatETA = (s: number) => {
  if (s <= 0 || s > 86400 * 30) return '—';
  const h = Math.floor(s / 3600);
  const m = Math.floor((s % 3600) / 60);
  if (h > 0) return `${h}h ${m}m`;
  if (m > 0) return `${m}m ${s % 60}s`;
  return `${s}s`;
};

export const ActiveTransfer: React.FC<ActiveTransferProps> = ({
  torrent,
  adapters,
  onPause,
  onResume,
  onRemove,
  onGetPieceStates,
}) => {
  const [pieceStates, setPieceStates] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [actionError, setActionError] = useState('');

  // Keep retrying snapshots, including when metadata arrives while paused.
  useEffect(() => {
    let alive = true;
    let pending = false;
    setPieceStates([]);
    const tick = async () => {
      if (pending) return;
      pending = true;
      try {
        const s = await onGetPieceStates(torrent.id);
        if (alive) setPieceStates(s ?? []);
      } catch {
        /* retry on next poll */
      } finally { pending = false; }
    };
    tick();

    const t = setInterval(tick, 1500);
    return () => {
      alive = false;
      clearInterval(t);
    };
  }, [torrent.id, onGetPieceStates]);

  const playPause = async () => {
    setBusy(true);
    setActionError('');
    try {
      torrent.status === 'paused' || torrent.status === 'error' ? await onResume(torrent.id) : await onPause(torrent.id);
    } catch (err) {
      setActionError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  };

  const remove = async () => {
    if (window.confirm(`Remove “${torrent.name}”?`)) {
      const del = window.confirm('Also delete downloaded files from disk?');
      setBusy(true);
      setActionError('');
      try { await onRemove(torrent.id, del); }
      catch (err) { setActionError(errorMessage(err)); }
      finally { setBusy(false); }
    }
  };

  const paused = torrent.status === 'paused';
  const downloading = torrent.status === 'downloading';
  const canResume = paused || torrent.status === 'error';
  const actionLabel = torrent.status === 'error' ? 'Retry' : paused ? 'Resume' : 'Pause';

  return (
    <article className="glass rise rounded-[24px] p-4 space-y-3.5">
      {/* Header */}
      <header className="flex items-center gap-3">
        <div
          className="w-10 h-10 rounded-[12px] flex items-center justify-center flex-shrink-0"
          style={{
            background: 'linear-gradient(180deg, rgba(64,211,196,.22), rgba(64,211,196,.08))',
            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.12)',
            color: 'var(--accent)',
          }}
        >
          <FileArchive className="w-[18px] h-[18px]" />
        </div>

        <div className="min-w-0 flex-1">
          <h3 className="text-[14px] font-semibold tracking-tight truncate" title={torrent.name}>
            {torrent.name}
          </h3>
          <div className="flex items-center gap-1.5 text-[11px] t-label-3 t-num">
            <span
              className={`w-1.5 h-1.5 rounded-full ${downloading ? 'live-dot' : ''}`}
              style={{ background: downloading ? 'var(--accent)' : paused ? '#f5b544' : 'var(--label-3)' }}
            />
            <span className="capitalize t-label-2">{torrent.status}</span>
            <span>·</span>
            <span>
              {formatBytes(torrent.downloadedBytes)} of {formatBytes(torrent.totalBytes || 0)}
            </span>
          </div>
        </div>

        {/* Big speed readout */}
        <div className="text-right flex-shrink-0 hidden sm:block">
          <div className="flex items-center justify-end gap-1 text-[18px] font-semibold tracking-tight t-num leading-none">
            <ArrowDown className="w-4 h-4" style={{ color: 'var(--accent)' }} strokeWidth={2.5} />
            {formatSpeed(torrent.downloadSpeed)}
          </div>
        </div>

        {/* Control capsule */}
        <div className="flex items-center gap-0.5 p-0.5 rounded-full bg-white/[0.05] border border-white/[0.06] flex-shrink-0">
          <button id={`btn-playpause-${torrent.id}`} onClick={playPause} disabled={busy} className="icon-btn" title={actionLabel} aria-label={actionLabel}>
            {canResume ? <Play className="w-3.5 h-3.5 fill-current" /> : <Pause className="w-3.5 h-3.5 fill-current" />}
          </button>
          <button id={`btn-remove-${torrent.id}`} onClick={remove} disabled={busy} className="icon-btn hover:!text-[#ff6961]" title="Remove" aria-label="Remove transfer">
            <Trash2 className="w-3.5 h-3.5" />
          </button>
        </div>
      </header>

      {(actionError || torrent.error) && <p role="alert" className="text-[12px] text-red-300">{actionError || torrent.error}</p>}
      {/* Piece map — default expanded dense grid */}
      <ChunkMatrix
        torrent={torrent}
        adapters={adapters}
        pieceStates={pieceStates}
      />

      {/* Metrics row */}
      <footer className="grid grid-cols-3 gap-2">
        {[
          { icon: <Users className="w-3.5 h-3.5" />, label: 'Peers', value: `${torrent.connectedPeers}`, sub: `/ ${torrent.totalPeers ?? '—'}` },
          { icon: <Clock className="w-3.5 h-3.5" />, label: 'Remaining', value: formatETA(torrent.etaSeconds), sub: '' },
          { icon: <ArrowDown className="w-3.5 h-3.5" />, label: 'Speed', value: formatSpeed(torrent.downloadSpeed), sub: '' },
        ].map(m => (
          <div key={m.label} className="glass-well rounded-[14px] px-3 py-2">
            <div className="flex items-center gap-1.5 t-eyebrow !text-[9.5px]">
              {m.icon}
              {m.label}
            </div>
            <div className="mt-0.5 text-[14px] font-semibold tracking-tight t-num">
              {m.value}
              {m.sub && <span className="text-[11px] font-medium t-label-3 ml-1">{m.sub}</span>}
            </div>
          </div>
        ))}
      </footer>
    </article>
  );
};
