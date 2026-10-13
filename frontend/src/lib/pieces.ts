export interface PieceGroup {
  start: number; end: number; status: number; verified: number; partial: number; checking: number;
}

export function buildPieceGroups(states: number[], limit: number): PieceGroup[] {
  const slots = Math.min(states.length, Math.max(1, Math.floor(limit) || 1));
  const groups: PieceGroup[] = [];
  for (let i = 0; i < slots; i++) {
    const start = Math.floor(i * states.length / slots);
    const end = Math.floor((i + 1) * states.length / slots) - 1;
    const slice = states.slice(start, end + 1);
    const verified = slice.filter(s => s === 2).length;
    const partial = slice.filter(s => s === 1).length;
    const checking = slice.filter(s => s === 3).length;
    const status = verified === slice.length ? 2 : checking ? 3 : partial ? 1 : 0;
    groups.push({ start, end, status, verified, partial, checking });
  }
  return groups;
}
