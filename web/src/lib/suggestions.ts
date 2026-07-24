import type { FileRef } from './diff';
import { riskOf } from './order';
import type { FileState, Section, SessionResponse } from './types';

export type SuggestionAction =
  | { kind: 'hide'; refs: FileRef[] }
  | { kind: 'review'; refs: FileRef[] }
  | { kind: 'reveal'; ref: FileRef };

// A diff-derived one-tap triage action. Every suggestion is deterministic and
// runs client-side (the ⚡ instant lane); a suggestion is only emitted when its
// count is non-zero, so an irrelevant chip is unrepresentable.
export interface Suggestion {
  id: string;
  label: string;
  count: number;
  action: SuggestionAction;
}

interface RefFile {
  ref: FileRef;
  state: FileState;
  risk: ReturnType<typeof riskOf>;
  generated: boolean;
  vendored: boolean;
}

function refFiles(sections: readonly Section[]): RefFile[] {
  return sections.flatMap((section) =>
    section.files.map((f) => ({
      ref: { sectionKey: section.sectionKey, path: f.path },
      state: section.fileStates[f.path] ?? { reviewed: false, hidden: false },
      risk: riskOf(section.organization, f.path),
      generated: !!f.generated,
      vendored: !!f.vendored,
    })),
  );
}

export function deriveSuggestions(session: SessionResponse): Suggestion[] {
  const files = refFiles(session.sections);
  const out: Suggestion[] = [];

  const highRisk = files.filter((f) => !f.state.hidden && !f.state.reviewed && f.risk === 'high');
  if (highRisk.length > 0) {
    out.push({
      id: 'open-high-risk',
      label: highRisk.length === 1 ? 'Open the high-risk file' : `Open first of ${highRisk.length} high-risk`,
      count: highRisk.length,
      action: { kind: 'reveal', ref: highRisk[0].ref },
    });
  }

  const mechanical = files.filter(
    (f) => !f.state.reviewed && !f.state.hidden && f.risk === 'mechanical',
  );
  if (mechanical.length > 0) {
    out.push({
      id: 'mark-mechanical',
      label: `Mark ${mechanical.length} mechanical viewed`,
      count: mechanical.length,
      action: { kind: 'review', refs: mechanical.map((f) => f.ref) },
    });
  }

  const noise = files.filter((f) => !f.state.hidden && (f.generated || f.vendored));
  if (noise.length > 0) {
    out.push({
      id: 'hide-noise',
      label: `Hide ${noise.length} generated ${noise.length === 1 ? 'file' : 'files'}`,
      count: noise.length,
      action: { kind: 'hide', refs: noise.map((f) => f.ref) },
    });
  }

  return out;
}
