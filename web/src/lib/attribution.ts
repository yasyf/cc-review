import type { AttributionRange, Turn } from './types';

export const TURN_PALETTE_SIZE = 8;

// Injected into each diff's shadow root via the CodeView `unsafeCSS` option;
// decorateContainer stamps the data attrs + --cc-turn-color these select on.
export const TURN_UNSAFE_CSS = `
[data-cc-turn] {
  box-shadow: inset 3px 0 0 0 var(--cc-turn-color);
}
[data-cc-dim] {
  opacity: 0.45;
}
`;

export interface TurnIndexEntry {
  seq: number;
  colorVar: number;
  turn: Turn;
}

export type TurnIndex = ReadonlyMap<string, TurnIndexEntry>;

export function buildTurnIndex(turns: readonly Turn[]): TurnIndex {
  return new Map(
    turns.map((turn, i) => [
      turn.id,
      { seq: i + 1, colorVar: (i + 1) % TURN_PALETTE_SIZE, turn },
    ]),
  );
}

export function turnIdAt(
  ranges: readonly AttributionRange[],
  line: number,
): string | undefined {
  let lo = 0;
  let hi = ranges.length - 1;
  while (lo <= hi) {
    const mid = (lo + hi) >> 1;
    const range = ranges[mid];
    if (line < range.start) hi = mid - 1;
    else if (line > range.end) lo = mid + 1;
    else return range.turnId;
  }
  return undefined;
}

// Idempotent: stamps the new-side addition rows of one rendered diff container
// and strips stale attrs from rows that no longer match, so it can re-run on
// every post-render and on activeTurnId changes.
export function decorateContainer(
  rootEl: HTMLElement,
  fileRanges: readonly AttributionRange[],
  turnIndex: TurnIndex,
  activeTurnId: string | null,
): void {
  const rows = rootEl.shadowRoot?.querySelectorAll<HTMLElement>(
    '[data-line][data-line-type="change-addition"]',
  );
  for (const row of rows ?? []) {
    const turnId = turnIdAt(fileRanges, Number(row.dataset.line));
    if (turnId) {
      const entry = turnIndex.get(turnId)!;
      row.dataset.ccTurn = turnId;
      row.style.setProperty('--cc-turn-color', `var(--turn-${entry.colorVar})`);
    } else {
      delete row.dataset.ccTurn;
      row.style.removeProperty('--cc-turn-color');
    }
    if (activeTurnId !== null && turnId !== activeTurnId) row.dataset.ccDim = '';
    else delete row.dataset.ccDim;
  }
}

export function firstOccurrence(
  attributions: Readonly<Record<string, readonly AttributionRange[]>>,
  turnId: string,
): { file: string; line: number } | null {
  for (const [file, ranges] of Object.entries(attributions)) {
    for (const range of ranges) {
      if (range.turnId === turnId) return { file, line: range.start };
    }
  }
  return null;
}
