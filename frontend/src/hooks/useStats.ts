import { useState, useEffect, useCallback } from 'react';
import type { DownloadStats, AdapterStats } from '../types';
import { emptyStats, normalizeStats, errorMessage } from '../lib/stats';

declare global {
  interface Window {
    runtime?: {
      EventsOn: (name: string, callback: (data: DownloadStats) => void) => () => void;
    };
    go?: { main: { App: {
      AddMagnet: (url: string) => Promise<string>;
      AddTorrentFile: () => Promise<string>;
      GetStats: () => Promise<DownloadStats>;
      RescanAdapters: () => Promise<AdapterStats[]>;
      ToggleAdapter: (id: string, enabled: boolean) => Promise<void>;
      PauseTorrent: (id: string) => Promise<void>;
      ResumeTorrent: (id: string) => Promise<void>;
      RemoveTorrent: (id: string, deleteData: boolean) => Promise<void>;
      GetPieceStates: (id: string) => Promise<number[]>;
    } } };
  }
}

function backend() {
  const app = window.go?.main?.App;
  if (!app) throw new Error('Open the desktop app to download torrents. Browser preview cannot start downloads.');
  return app;
}

export function useStats() {
  const [stats, setStats] = useState<DownloadStats>(emptyStats);
  const [startupError, setStartupError] = useState('');
  const isWailsRuntime = !!window.go?.main?.App;

  useEffect(() => {
    if (!isWailsRuntime) return;
    let alive = true;
    let receivedEvent = false;
    const unsubscribe = window.runtime?.EventsOn('stats:update', data => {
      receivedEvent = true;
      if (alive) {
        setStats(normalizeStats(data));
        setStartupError(data?.error || '');
      }
    });
    backend().GetStats().then(data => {
      if (alive) {
        if (!receivedEvent) setStats(normalizeStats(data));
        if (!receivedEvent) setStartupError(data?.error || '');
      }
    }).catch(err => { if (alive) setStartupError(errorMessage(err)); });
    return () => { alive = false; unsubscribe?.(); };
  }, [isWailsRuntime]);

  const addMagnet = useCallback((url: string) => backend().AddMagnet(url), []);
  const addTorrentFile = useCallback(() => backend().AddTorrentFile(), []);
  const toggleAdapter = useCallback((id: string, enabled: boolean) => backend().ToggleAdapter(id, enabled), []);
  const pauseTorrent = useCallback((id: string) => backend().PauseTorrent(id), []);
  const resumeTorrent = useCallback((id: string) => backend().ResumeTorrent(id), []);
  const removeTorrent = useCallback((id: string, deleteData: boolean) => backend().RemoveTorrent(id, deleteData), []);
  const getPieceStates = useCallback(async (id: string) => (await backend().GetPieceStates(id)) ?? [], []);
  const rescanAdapters = useCallback(async () => {
    const adapters = (await backend().RescanAdapters()) ?? [];
    setStats(prev => ({ ...prev, adapters }));
  }, []);
  return { stats, isWailsRuntime, startupError,
    addMagnet, addTorrentFile, toggleAdapter, rescanAdapters, pauseTorrent, resumeTorrent, removeTorrent, getPieceStates };
}
