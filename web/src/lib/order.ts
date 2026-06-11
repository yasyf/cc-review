// Single owner of file ordering across the diff, the comments panel, and the
// chapter panel.

import type { ChapterFile, Organization, Risk, SessionResponse } from './types';
import type { ViewMode } from './view-prefs';

const RISK_RANK: Record<Risk, number> = { high: 0, medium: 1, low: 2, mechanical: 3 };

export interface TodoGroup {
  title: string;
  files: ChapterFile[];
}

export function riskOf(organization: Organization | null, path: string): Risk | null {
  if (!organization) return null;
  for (const chapter of organization.chapters) {
    const file = chapter.files.find((f) => f.path === path);
    if (file) return file.risk;
  }
  return null;
}

// Chapters ranked scariest-first: best (lowest) risk rank, then high count,
// then medium count, then submitted chapter order. Within a chapter the stable
// sort keeps Claude's submitted order inside each risk tier — that order IS
// the rank. Paths deduped across chapters (first occurrence wins, matching
// riskOf), restricted to `present`, empty chapters dropped.
export function todoGroups(organization: Organization, present: ReadonlySet<string>): TodoGroup[] {
  const seen = new Set<string>();
  const ranked = organization.chapters.flatMap((chapter, index) => {
    const files = chapter.files.filter((f) => {
      if (seen.has(f.path) || !present.has(f.path)) return false;
      seen.add(f.path);
      return true;
    });
    if (files.length === 0) return [];
    files.sort((a, b) => RISK_RANK[a.risk] - RISK_RANK[b.risk]);
    return [
      {
        title: chapter.title,
        files,
        index,
        best: Math.min(...files.map((f) => RISK_RANK[f.risk])),
        high: files.filter((f) => f.risk === 'high').length,
        medium: files.filter((f) => f.risk === 'medium').length,
      },
    ];
  });
  ranked.sort(
    (a, b) => a.best - b.best || b.high - a.high || b.medium - a.medium || a.index - b.index,
  );
  return ranked.map(({ title, files }) => ({ title, files }));
}

// Path → rank for the active view mode. Files the organization missed sort
// after every organized file, in patch order; without an organization every
// mode degrades to patch order.
export function fileOrder(session: SessionResponse, mode: ViewMode): Map<string, number> {
  const patchOrder = session.files.map((f) => f.path);
  const org = session.organization;

  if (mode === 'story' && org) {
    const ordered = org.chapters.flatMap((c) => c.files.map((f) => f.path));
    const inChapters = new Set(ordered);
    ordered.push(...patchOrder.filter((path) => !inChapters.has(path)));
    return new Map(ordered.map((path, i) => [path, i]));
  }

  if (mode === 'todo' && org) {
    const ordered = todoGroups(org, new Set(patchOrder)).flatMap((g) =>
      g.files.map((f) => f.path),
    );
    const organized = new Set(ordered);
    ordered.push(...patchOrder.filter((path) => !organized.has(path)));
    return new Map(ordered.map((path, i) => [path, i]));
  }

  return new Map(patchOrder.map((path, i) => [path, i]));
}
