import React from 'react';
import { Wifi, Cable, Smartphone, Check, Network } from 'lucide-react';
import { AdapterStats } from '../types';

interface NetworkCardProps {
  adapter: AdapterStats;
  onToggle: (id: string, enabled: boolean) => void;
}

const formatSpeed = (b: number): string => {
  if (b >= 1024 * 1024) return `${(b / (1024 * 1024)).toFixed(1)} MB/s`;
  if (b >= 1024) return `${(b / 1024).toFixed(0)} KB/s`;
  return `${b} B/s`;
};

/** Ping quality → 0..3 bars */
const pingBars = (ms?: number) => {
  if (!ms || ms <= 0) return 0;
  if (ms < 30) return 3;
  if (ms < 80) return 2;
  return 1;
};

export const NetworkCard: React.FC<NetworkCardProps> = ({ adapter, onToggle }) => {
  const isSelected = adapter.enabled && adapter.active;
  const color = adapter.color || '#40d3c4';
  const ifName = adapter.interfaceName || adapter.name;
  const displayName = adapter.name.split(' (')[0] || adapter.name;
  const reachable = !!adapter.pingMs && adapter.pingMs > 0;
  const bars = pingBars(adapter.pingMs);

  const Icon = adapter.type === 'wifi' ? Wifi : adapter.type === 'cellular' ? Smartphone : adapter.type === 'ethernet' ? Cable : Network;
  const typeLabel = adapter.type === 'wifi' ? 'Wi‑Fi' : adapter.type === 'cellular' ? 'Cellular' : adapter.type === 'ethernet' ? 'Ethernet' : 'Network';

  return (
    <button
      type="button"
      id={`network-card-${adapter.id}`}
      aria-pressed={isSelected}
      onClick={() => onToggle(adapter.id, !adapter.enabled)}
      className={`glass rise group text-left rounded-[20px] p-3.5 transition-all duration-300 ease-[cubic-bezier(0.32,0.72,0,1)] hover:-translate-y-0.5 focus:outline-none focus-visible:ring-2 focus-visible:ring-[var(--accent-ring)] ${
        isSelected ? '' : 'opacity-60 hover:opacity-100'
      }`}
      style={
        isSelected
          ? {
              borderColor: `${color}66`,
              boxShadow: `inset 0 1px 0 rgba(255,255,255,.12), 0 0 0 1px ${color}33, 0 14px 36px -14px ${color}66`,
            }
          : undefined
      }
    >
      {/* Header: icon tile · name · check */}
      <div className="flex items-start gap-2.5">
        <div
          className="w-8 h-8 rounded-[10px] flex items-center justify-center flex-shrink-0 transition-colors"
          style={{
            background: isSelected ? `${color}22` : 'rgba(255,255,255,0.06)',
            color: isSelected ? color : 'var(--label-2)',
            boxShadow: 'inset 0 1px 0 rgba(255,255,255,.08)',
          }}
        >
          <Icon className="w-4 h-4" strokeWidth={2} />
        </div>

        <div className="min-w-0 flex-1">
          <div className="text-[13px] font-semibold tracking-tight truncate">{displayName}</div>
          <div className="text-[11px] t-label-3 truncate t-num">
            {typeLabel} · {ifName}
          </div>
        </div>

        <div
          className={`w-[18px] h-[18px] rounded-full flex items-center justify-center flex-shrink-0 transition-all duration-300 ${
            isSelected ? 'scale-100' : 'scale-90 border border-white/20'
          }`}
          style={isSelected ? { background: color, boxShadow: `0 0 10px ${color}88` } : undefined}
        >
          {isSelected && <Check className="w-3 h-3 text-black" strokeWidth={3.5} />}
        </div>
      </div>

      {/* IP */}
      <div className="mt-3 text-[11px] font-mono t-label-2 truncate">{adapter.ip}</div>

      {/* Footer: ping + bars · speed */}
      <div className="mt-2.5 pt-2.5 border-t flex items-end justify-between">
        <div>
          <div className="t-eyebrow !text-[9.5px]">Ping</div>
          <div className="text-[15px] font-semibold tracking-tight t-num leading-tight">
            {reachable ? (
              <>
                {adapter.pingMs}
                <span className="text-[11px] font-medium t-label-3 ml-0.5">ms</span>
              </>
            ) : (
              <span className="t-label-3">—</span>
            )}
          </div>
        </div>

        <div className="flex items-end gap-2">
          {isSelected && adapter.downloadSpeed > 0 && (
            <span className="text-[11px] font-semibold t-num" style={{ color }}>
              {formatSpeed(adapter.downloadSpeed)}
            </span>
          )}
          <div className="flex items-end gap-[2px] h-3.5" aria-label={`Signal ${bars} of 3`}>
            {[1, 2, 3].map(i => (
              <span
                key={i}
                className="w-[3px] rounded-full transition-colors"
                style={{
                  height: `${i * 33}%`,
                  background: i <= bars ? (isSelected ? color : 'var(--label-2)') : 'var(--label-4)',
                }}
              />
            ))}
          </div>
        </div>
      </div>
    </button>
  );
};
