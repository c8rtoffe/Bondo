import { test } from 'node:test';
import assert from 'node:assert/strict';
import { normalizeStats, errorMessage } from '../src/lib/stats.ts';
import { buildPieceGroups } from '../src/lib/pieces.ts';

test('null backend arrays and invalid counters are safe', () => {
  const s = normalizeStats({ adapters: null, torrents: null, peerDiagnostics: null, totalDownloadSpeed: NaN });
  assert.deepEqual(s.adapters, []);
  assert.deepEqual(s.torrents, []);
  assert.deepEqual(s.peerDiagnostics, []);
  assert.equal(s.totalDownloadSpeed, 0);
  assert.equal(errorMessage('Go returned an error'), 'Go returned an error');
});

test('piece groups cover every real piece and preserve verified counts', () => {
  const states = Array.from({ length: 2918 }, (_, i) => i % 4);
  const groups = buildPieceGroups(states, 320);
  assert.equal(groups.length, 320);
  assert.equal(groups[0].start, 0);
  assert.equal(groups.at(-1).end, states.length - 1);
  for (let i = 1; i < groups.length; i++) assert.equal(groups[i].start, groups[i-1].end + 1);
  assert.equal(groups.reduce((s,g) => s+g.verified, 0), states.filter(s => s===2).length);
  assert.deepEqual(buildPieceGroups([], 320), []);
  assert.equal(buildPieceGroups([2,0], 320)[1].status, 0);
  assert.equal(buildPieceGroups([2,0], 1)[0].status, 0);
});
