import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { fileURLToPath } from 'node:url';
import { build } from 'esbuild';
import React from 'react';
import { renderToString } from 'react-dom/server';

const require = createRequire(import.meta.url);
async function component(file, exportedName) {
  const result = await build({ entryPoints: [fileURLToPath(new URL(file, import.meta.url))], bundle: true,
    platform: 'node', format: 'cjs', external: ['react', 'react-dom'], write: false });
  const module = { exports: {} };
  new Function('require', 'module', 'exports', result.outputFiles[0].text)(require, module, module.exports);
  return module.exports[exportedName];
}

test('browser preview renders empty state and does not report fake downloads', async () => {
  const App = await component('../src/App.tsx', 'App');
  globalThis.window = {};
  try {
    const html = renderToString(React.createElement(App));
    assert.ok(html.includes('Browser preview: open the desktop app'));
    assert.ok(html.includes('Nothing downloading'));
    assert.ok(!html.includes('Change folder'));
    assert.ok(!html.includes('btn-paste'));
    assert.ok(!html.includes('btn-magnet-settings'));
    assert.ok(!html.includes('Magnet integration settings'));
    assert.ok(html.includes('Add torrent file'));
    assert.ok(html.includes('+ Torrent'));
    assert.ok(!html.includes('ubuntu-24.04'));
    assert.ok(!html.includes('14.85'));
  } finally { delete globalThis.window; }
});

test('opened routing dialog handles null peer list', async () => {
  const Modal = await component('../src/components/PeerDiagnosticsModal.tsx', 'PeerDiagnosticsModal');
  const html = renderToString(React.createElement(Modal, { isOpen: true, onClose() {}, diagnostics: null, adapters: [] }));
  assert.ok(html.includes('No active peer TCP sockets found'));
});

 test('a disk-error transfer offers Retry instead of an unusable Pause action', async () => {
  const Transfer = await component('../src/components/ActiveTransfer.tsx', 'ActiveTransfer');
  const torrent = { id: 'failed', name: 'Download', status: 'error', error: 'Disk full',
    progress: 0, downloadedBytes: 0, totalBytes: 4, downloadSpeed: 0, connectedPeers: 0,
    totalPeers: 0, etaSeconds: 0, totalPieces: 1 };
  const html = renderToString(React.createElement(Transfer, { torrent, adapters: [],
    onPause: async () => {}, onResume: async () => {}, onRemove: async () => {}, onGetPieceStates: async () => [] }));
  assert.ok(html.includes('aria-label="Retry"'));
  assert.ok(html.includes('Disk full'));
  assert.ok(!html.includes('title="Pause"'));
});