import React, { useState, useMemo } from 'react';
import type { TorrentInfo, AdapterStats } from '../types';
import { buildPieceGroups } from '../lib/pieces';

interface ChunkMatrixProps {
  torrent: TorrentInfo;
  adapters: AdapterStats[];
  pieceStates?: number[];
}

const colors = ['rgba(255,255,255,.08)', '#06b6d4', '#10b981', '#f5b544'];
const labels = ['Pending', 'Partial', 'Verified', 'Checking'];

// Group actual backend states without inventing download order or adapter provenance.
export const ChunkMatrix: React.FC<ChunkMatrixProps> = ({ torrent, adapters, pieceStates = [] }) => {
  const [hoveredIndex, setHoveredIndex] = useState<number | null>(null);
  const groups = useMemo(() => buildPieceGroups(pieceStates, 320), [pieceStates]);
  const group = hoveredIndex === null ? undefined : groups[hoveredIndex];
  const verified = pieceStates.filter(s => s === 2).length;
  return (
    <div className="space-y-2">
      {groups.length ? (
        <div className="glass-well rounded-[12px] p-[5px] max-h-52 overflow-y-auto">
          <div className="grid grid-cols-[repeat(auto-fill,minmax(12px,1fr))] gap-1">
            {groups.map((g, index) => (
              <div key={g.start} onMouseEnter={() => setHoveredIndex(index)} onMouseLeave={() => setHoveredIndex(null)}
                title={`Pieces ${g.start}-${g.end}: ${g.verified}/${g.end-g.start+1} verified; ${labels[g.status]}`}
                className="h-3.5 rounded-[3px] relative overflow-hidden"
                style={{ background: colors[g.status] }}>
                {g.status !== 2 && g.verified > 0 && <span className="absolute inset-y-0 left-0" style={{ background: colors[2], width: `${g.verified/(g.end-g.start+1)*100}%` }} />}
              </div>
            ))}
          </div>
        </div>
      ) : <p className="glass-well rounded-[12px] p-3 text-[11px] t-label-3">Piece information is not available yet.</p>}
      <div className="flex flex-wrap justify-between gap-2 text-[11px] t-label-2">
        <span>{group ? `Pieces ${group.start}-${group.end}: ${group.verified} verified, ${group.partial} partial, ${group.checking} checking` : labels.map((label, i) => <span key={label} className="inline-flex items-center gap-1 mr-3"><span className="w-2 h-2 rounded-sm" style={{ background: colors[i] }} />{label}</span>)}</span>
        <span>{torrent.progress.toFixed(1)}% - {verified}/{pieceStates.length || torrent.totalPieces} pieces verified</span>
      </div>
      <div className="flex flex-wrap gap-3 text-[11px] t-label-2">
        <span className="t-label-3">Network traffic - all transfers</span>
        {adapters.filter(a => a.active && a.enabled).map(a => <span key={a.id} style={{ color: a.color }}>{a.interfaceName || a.name}: {(a.downloadSpeed/1048576).toFixed(2)} MB/s</span>)}
      </div>
    </div>
  );
};
