import { riskOf } from './order';
import type { SessionResponse } from './types';

export type SuggestionAction =
  | { kind: 'hide'; paths: string[] }
  | { kind: 'review'; paths: string[] }
  | { kind: 'reveal'; path: string };

// A diff-derived one-tap triage action. Every suggestion is deterministic and
// runs client-side (the ⚡ instant lane); a suggestion is only emitted when its
// count is non-zero, so an irrelevant chip is unrepresentable.
export interface Suggestion {
  id: string;
  label: string;
  count: number;
  action: SuggestionAction;
}

export function deriveSuggestions(session: SessionResponse): Suggestion[] {
  const { files, fileStates, organization } = session;
  const stateOf = (p: string) => fileStates[p] ?? { reviewed: false, hidden: false };
  const out: Suggestion[] = [];

  const highRisk = files.filter(
    (f) => !stateOf(f.path).hidden && !stateOf(f.path).reviewed && riskOf(organization, f.path) === 'high',
  );
  if (highRisk.length > 0) {
    out.push({
      id: 'open-high-risk',
      label: highRisk.length === 1 ? 'Open the high-risk file' : `Open first of ${highRisk.length} high-risk`,
      count: highRisk.length,
      action: { kind: 'reveal', path: highRisk[0].path },
    });
  }

  const mechanical = files.filter(
    (f) => !stateOf(f.path).reviewed && !stateOf(f.path).hidden && riskOf(organization, f.path) === 'mechanical',
  );
  if (mechanical.length > 0) {
    out.push({
      id: 'mark-mechanical',
      label: `Mark ${mechanical.length} mechanical viewed`,
      count: mechanical.length,
      action: { kind: 'review', paths: mechanical.map((f) => f.path) },
    });
  }

  const noise = files.filter((f) => !stateOf(f.path).hidden && (f.generated || f.vendored));
  if (noise.length > 0) {
    out.push({
      id: 'hide-noise',
      label: `Hide ${noise.length} generated ${noise.length === 1 ? 'file' : 'files'}`,
      count: noise.length,
      action: { kind: 'hide', paths: noise.map((f) => f.path) },
    });
  }

  return out;
}
