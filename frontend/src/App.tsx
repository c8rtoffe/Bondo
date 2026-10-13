import React, { useEffect, useState } from 'react';
import {
  Link2,
  RotateCw,
  ArrowDown,
  CheckCircle2,
  AlertCircle,
  Activity,
} from 'lucide-react';
import { errorMessage } from './lib/stats';
import { useStats } from './hooks/useStats';
import { NetworkCard } from './components/NetworkCard';
import { ActiveTransfer } from './components/ActiveTransfer';
import { PeerDiagnosticsModal } from './components/PeerDiagnosticsModal';

const formatSpeed = (b: number) =>
  b >= 1048576 ? `${(b / 1048576).toFixed(1)} MB/s` : b >= 1024 ? `${(b / 1024).toFixed(0)} KB/s` : `${b} B/s`;

export const App: React.FC = () => {
  const {
    stats,
    isWailsRuntime,
    startupError,
    addMagnet,
    addTorrentFile,
    toggleAdapter,
    rescanAdapters,
    pauseTorrent,
    resumeTorrent,
    removeTorrent,
    getPieceStates,
  } = useStats();

  const [linkInput, setLinkInput] = useState('');
  const [sourceAction, setSourceAction] = useState<'link' | 'file' | null>(null);
  const isChecking = sourceAction !== null;
  const [feedback, setFeedback] = useState<{ type: 'error' | 'success'; message: string } | null>(null);
  const [isDiagModalOpen, setIsDiagModalOpen] = useState(false);
  const [isRescanning, setIsRescanning] = useState(false);

  useEffect(() => {
    if (feedback?.type !== 'success') return;
    const timer = setTimeout(() => setFeedback(null), 3000);
    return () => clearTimeout(timer);
  }, [feedback]);

  const selectedAdapters = stats.adapters.filter(a => a.enabled && a.active);
  const activeStreams = stats.torrents.reduce((acc, t) => acc + (t.connectedPeers || 0), 0);

  const handleStart = async (e?: React.FormEvent) => {
    e?.preventDefault();
    if (isChecking) return;
    const url = linkInput.trim();
    if (!url) {
      setFeedback({ type: 'error', message: 'Paste a magnet or torrent link to begin.' });
      return;
    }
    if (!isWailsRuntime || selectedAdapters.length === 0) {
      setFeedback({ type: 'error', message: 'Open the desktop app and select at least one network.' });
      return;
    }
    setSourceAction('link');
    setFeedback(null);
    try {
      await addMagnet(url);
      setFeedback({ type: 'success', message: 'Torrent added. Connecting to peers.' });
      setLinkInput('');
    } catch (err) {
      setFeedback({ type: 'error', message: errorMessage(err) });
    } finally {
      setSourceAction(null);
    }
  };

  const handleTorrentFile = async () => {
    if (isChecking) return;
    if (!isWailsRuntime || selectedAdapters.length === 0) {
      setFeedback({ type: 'error', message: 'Open the desktop app and select at least one network.' });
      return;
    }
    setSourceAction('file');
    setFeedback(null);
    try {
      const id = await addTorrentFile();
      if (id) {
        setFeedback({ type: 'success', message: 'Torrent file added. Connecting to peers.' });
      }
    } catch (err) {
      setFeedback({ type: 'error', message: errorMessage(err) });
    } finally {
      setSourceAction(null);
    }
  };

  const handleRescan = async () => {
    setIsRescanning(true);
    try {
      await rescanAdapters();
    } catch (err) {
      setFeedback({ type: 'error', message: errorMessage(err) });
    } finally {
      setIsRescanning(false);
    }
  };

  const handleToggle = async (id: string, enabled: boolean) => {
    try { await toggleAdapter(id, enabled); }
    catch (err) { setFeedback({ type: 'error', message: errorMessage(err) }); }
  };

  return (
    <div className="relative flex flex-col h-screen overflow-hidden select-none">
      <div className="ambient" aria-hidden />
      <div className="grain" aria-hidden />

      <h1 className="sr-only">Bondo — Multipath Torrent Downloader</h1>

      {/* Content */}
      <main className="relative z-10 flex-1 overflow-y-auto px-5 pt-5 pb-6 space-y-6">
        {/* ── Source ─────────────────────────────────────────────── */}
        <section className="glass rise rounded-[24px] p-2 space-y-1.5" aria-label="Download source">
          <form onSubmit={handleStart} className="flex flex-wrap sm:flex-nowrap items-center gap-2">
            <label className="glass-well w-full sm:w-auto sm:flex-1 flex items-center gap-2.5 rounded-[18px] h-11 px-3.5 min-w-0">
              <Link2 className="w-4 h-4 t-label-3 flex-shrink-0" />
              <input
                id="link-input"
                type="text"
                aria-label="Magnet link, infohash, or torrent file URL"
                value={linkInput}
                onChange={e => {
                  setLinkInput(e.target.value);
                  if (feedback) setFeedback(null);
                }}
                placeholder="Magnet link, infohash, or torrent file URL"
                className="flex-1 bg-transparent text-[13px] placeholder:text-[color:var(--label-3)] focus:outline-none min-w-0"
                autoComplete="off"
                spellCheck={false}
              />
            </label>

            <button type="button" id="btn-add-torrent-file" aria-label="Add torrent file" title="Add torrent file" onClick={handleTorrentFile} disabled={isChecking || !isWailsRuntime || selectedAdapters.length === 0} className="btn btn-glass !h-11 !px-3 !text-[12px] !rounded-[18px] flex-1 sm:flex-none">
              {sourceAction === 'file' ? (
                <><span className="w-3.5 h-3.5 border-2 border-current border-t-transparent rounded-full animate-spin" />Opening…</>
              ) : (
                '+ Torrent'
              )}
            </button>

            <button type="submit" id="btn-start" disabled={isChecking || !isWailsRuntime || selectedAdapters.length === 0} className="btn btn-primary !h-11 !px-5 !text-[13px] !rounded-[18px] flex-1 sm:flex-none">
              {sourceAction === 'link' ? (
                <>
                  <span className="w-3.5 h-3.5 border-2 border-current border-t-transparent rounded-full animate-spin" />
                  Checking
                </>
              ) : (
                <>
                  <ArrowDown className="w-4 h-4" strokeWidth={2.5} />
                  Download
                </>
              )}
            </button>
          </form>
        </section>
        {(!isWailsRuntime || startupError) && (
          <p role="status" className="text-[12px] text-amber-300 px-3">
            {startupError || 'Browser preview: open the desktop app to download torrents.'}
          </p>
        )}

        {feedback && (
          <div
            role="status"
            className="rise -mt-3 flex items-center gap-2 px-3.5 py-2 rounded-full w-fit text-[12px] font-medium"
            style={{
              background: feedback.type === 'error' ? 'rgba(255,105,97,.12)' : 'var(--accent-soft)',
              color: feedback.type === 'error' ? '#ff8a84' : 'var(--accent)',
            }}
          >
            {feedback.type === 'error' ? <AlertCircle className="w-3.5 h-3.5" /> : <CheckCircle2 className="w-3.5 h-3.5" />}
            {feedback.message}
          </div>
        )}

        {/* ── Networks ───────────────────────────────────────────── */}
        <section className="space-y-3" aria-labelledby="networks-heading">
          <div className="flex items-end justify-between px-1">
            <div className="flex items-center gap-2">
              <h2 id="networks-heading" className="text-[17px] font-semibold tracking-tight">
                Networks
              </h2>
              <button id="btn-rescan" onClick={handleRescan} disabled={isRescanning || !isWailsRuntime} className="icon-btn !w-6 !h-6" title="Rescan interfaces">
                <RotateCw className={`w-3.5 h-3.5 ${isRescanning ? 'animate-spin' : ''}`} />
              </button>
            </div>
            <span className="text-[12px] t-label-3 t-num">
              {stats.adapters.length} detected · <span className="t-label-2">{selectedAdapters.length} selected</span>
            </span>
          </div>

          <div className="grid grid-cols-1 sm:grid-cols-2 md:grid-cols-3 gap-3">
            {stats.adapters.map(a => (
              <NetworkCard key={a.id} adapter={a} onToggle={handleToggle} />
            ))}
          </div>
        </section>

        {/* ── Transfers ──────────────────────────────────────────── */}
        <section className="space-y-3" aria-labelledby="transfers-heading">
          <h2 id="transfers-heading" className="text-[17px] font-semibold tracking-tight px-1">
            Transfers
          </h2>

          {stats.torrents.length === 0 ? (
            <div className="glass rise rounded-[24px] px-6 py-8 text-center">
              <p className="text-[14px] font-semibold tracking-tight">Nothing downloading</p>
              <p className="text-[12px] t-label-3 mt-1">Enter a magnet link, infohash, or torrent URL, or add a .torrent file.</p>
            </div>
          ) : (
            stats.torrents.map(t => (
              <ActiveTransfer
                key={t.id}
                torrent={t}
                adapters={stats.adapters}
                onPause={pauseTorrent}
                onResume={resumeTorrent}
                onRemove={removeTorrent}
                onGetPieceStates={getPieceStates}
              />
            ))
          )}
        </section>
      </main>

      {/* ── Status bar ─────────────────────────────────────────── */}
      <footer className="glass-bar relative z-10 h-10 px-5 flex items-center justify-between text-[12px] t-label-2 t-num flex-shrink-0">
        <div className="flex items-center gap-2">
          <span
            className={`w-1.5 h-1.5 rounded-full ${selectedAdapters.length ? 'live-dot' : ''}`}
            style={{ background: selectedAdapters.length ? 'var(--accent)' : 'var(--label-3)' }}
          />
          <span>
            {selectedAdapters.length} network{selectedAdapters.length === 1 ? '' : 's'} selected
            <span className="t-label-3"> · </span>
            {activeStreams} connected peers
          </span>
        </div>

        <div className="flex items-center gap-2">
          {stats.totalDownloadSpeed > 0 && (
            <span className="flex items-center gap-1 font-semibold" style={{ color: 'var(--accent)' }}>
              <ArrowDown className="w-3.5 h-3.5" strokeWidth={2.5} />
              {formatSpeed(stats.totalDownloadSpeed)}
            </span>
          )}
          <button id="btn-diagnostics" onClick={() => setIsDiagModalOpen(true)} className="icon-btn !w-7 !h-7" title="Routing diagnostics" aria-label="Routing diagnostics" aria-haspopup="dialog">
            <Activity className="w-3.5 h-3.5" />
          </button>
        </div>
      </footer>

      <PeerDiagnosticsModal
        isOpen={isDiagModalOpen}
        onClose={() => setIsDiagModalOpen(false)}
        diagnostics={stats.peerDiagnostics}
        adapters={stats.adapters}
      />
    </div>
  );
};

export default App;
