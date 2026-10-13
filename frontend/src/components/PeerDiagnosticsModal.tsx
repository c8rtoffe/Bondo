import React, { useEffect, useRef, useState } from 'react';
import { X, Network, Search } from 'lucide-react';
import { PeerDiagnostic, AdapterStats } from '../types';


interface PeerDiagnosticsModalProps {
  isOpen: boolean;
  onClose: () => void;
  diagnostics: PeerDiagnostic[];
  adapters: AdapterStats[];
}

const formatBytes = (value: number): string => {
  const bytes = Number.isFinite(value) ? Math.max(0, value) : 0;
  if (bytes >= 1024 ** 3) return `${(bytes / 1024 ** 3).toFixed(1)} GB`;
  if (bytes >= 1024 ** 2) return `${(bytes / 1024 ** 2).toFixed(1)} MB`;
  if (bytes >= 1024) return `${(bytes / 1024).toFixed(0)} KB`;
  return `${bytes} B`;
};

export const PeerDiagnosticsModal: React.FC<PeerDiagnosticsModalProps> = ({
  isOpen, onClose, diagnostics, adapters,
}) => {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const [selectedAdapterId, setSelectedAdapterId] = useState('all');
  const [search, setSearch] = useState('');
  const routes = diagnostics ?? [];
  const networks = adapters ?? [];

  useEffect(() => {
    if (!isOpen) return;
    const dialog = dialogRef.current;
    const previousFocus = document.activeElement;
    dialog?.showModal();
    return () => {
      dialog?.close();
      if (previousFocus instanceof HTMLElement && previousFocus.isConnected) previousFocus.focus();
    };
  }, [isOpen]);

  useEffect(() => {
    if (selectedAdapterId !== 'all' && !networks.some(a => a.id === selectedAdapterId)) setSelectedAdapterId('all');
  }, [adapters, selectedAdapterId]);

  if (!isOpen) return null;

  const query = search.trim().toLowerCase();
  const filtered = routes.filter(item =>
    (selectedAdapterId === 'all' || item.adapterId === selectedAdapterId) &&
    (!query || [item.peerIp, item.adapterName, item.adapterIp, item.client || 'BitTorrent Peer']
      .some(value => (value || '').toLowerCase().includes(query)))
  );
  const resetFilters = () => { setSelectedAdapterId('all'); setSearch(''); };

  return (
    <dialog ref={dialogRef} className="routing-dialog" aria-modal="true"
      aria-labelledby="routing-title" aria-describedby="routing-description"
      onCancel={event => { event.preventDefault(); onClose(); }}
      onClick={event => {
        if (event.target !== event.currentTarget) return;
        const bounds = event.currentTarget.getBoundingClientRect();
        if (event.clientX < bounds.left || event.clientX > bounds.right ||
            event.clientY < bounds.top || event.clientY > bounds.bottom) onClose();
      }}>
      <header className="routing-header">
        <div className="routing-heading">
          <div>
            <h2 id="routing-title">Routing diagnostics</h2>
            <p id="routing-description">See which network each outgoing peer connection uses.</p>
          </div>
        </div>
        <button type="button" className="routing-close" onClick={onClose} aria-label="Close routing diagnostics">
          <X size={18} strokeWidth={1.7} />
        </button>
      </header>

      <div className="routing-toolbar">
        <label className="routing-network">
          <span className="sr-only">Filter by network adapter</span>
          <select value={selectedAdapterId} onChange={event => setSelectedAdapterId(event.target.value)}>
            <option value="all">All networks</option>
            {networks.map(adapter => <option key={adapter.id} value={adapter.id}>{adapter.name} ({adapter.ip})</option>)}
          </select>
        </label>
        <div className="routing-search">
          <Search size={16} strokeWidth={1.8} aria-hidden="true" />
          <input type="search" aria-label="Search connections by IP, network, or client"
            placeholder="Search IP, network, or client" value={search}
            onChange={event => setSearch(event.target.value)} autoComplete="off" spellCheck={false} />
          {search && <button type="button" onClick={() => setSearch('')} aria-label="Clear search"><X size={14} /></button>}
        </div>
      </div>

      <div className="routing-content" tabIndex={0} role="region" aria-label="Peer connection routes">
        {filtered.length === 0 ? (
          <div className="routing-empty">
            <Network size={26} strokeWidth={1.4} aria-hidden="true" />
            <h3>{routes.length ? 'No matching connections' : 'No active connections'}</h3>
            <p>{routes.length ? 'Try another network or search term.' : 'No active peer TCP sockets found. Start a transfer to see its routes.'}</p>
            {routes.length > 0 && <button type="button" className="routing-reset" onClick={resetFilters}>Clear filters</button>}
          </div>
        ) : (
          <table className="routing-table">
            <caption className="sr-only">Outgoing peer connections and their bound local networks. Received socket bytes include protocol traffic.</caption>
            <thead><tr>
              <th scope="col">Peer endpoint</th><th scope="col">Network</th>
              <th scope="col">Source IP</th><th scope="col">Client</th>
              <th scope="col" className="routing-bytes">Received</th>
            </tr></thead>
            <tbody>{filtered.map(item => {
              const adapter = networks.find(network => network.id === item.adapterId);
              return (
                <tr key={item.id || `${item.peerIp}-${item.adapterId}-${item.adapterIp}-${item.connectedAt}`}>
                  <td className="routing-endpoint">{item.peerIp}</td>
                  <td><span className="routing-adapter" title={item.adapterName}>
                    <span className="routing-adapter-dot" style={{ background: adapter?.color || 'var(--accent)' }} aria-hidden="true" />
                    <span>{item.adapterName}</span>
                  </span></td>
                  <td className="routing-ip">{item.adapterIp}</td>
                  <td><span className="routing-client" title={item.client || 'BitTorrent Peer'}>{item.client || 'BitTorrent Peer'}</span></td>
                  <td className="routing-bytes">{formatBytes(item.bytesDownloaded)}</td>
                </tr>
              );
            })}</tbody>
          </table>
        )}
      </div>

      <footer className="routing-footer">
        <span>Socket bytes include protocol traffic.</span>
        <span className="routing-count" role="status">
          {filtered.length === routes.length ? `${routes.length} connection${routes.length === 1 ? '' : 's'}` : `${filtered.length} of ${routes.length} connections`}
        </span>
      </footer>
    </dialog>
  );
};
