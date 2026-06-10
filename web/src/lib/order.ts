// Single owner of file ordering across the diff, the comments panel, and the
// chapter panel.

import type { Chapter, Organization, Risk, SessionResponse } from './types';
import type { ViewMode } from './view-prefs';

export const RISK_LEVELS: readonly Risk[] = ['high', 'medium', 'low', 'mechanical'];

const RISK_RANK: Record<Risk, number> = { high: 0, medium: 1, low: 2, mechanical: 3 };

export function riskOf(organization: Organization | null, path: string): Risk | null {
  if (!organization) return null;
  for (const chapter of organization.chapters) {
    const file = chapter.files.find((f) => f.path === path);
    if (file) return file.risk;
  }
  return null;
}

export function chapterOf(organization: Organization | null, path: string): Chapter | null {
  if (!organization) return null;
  return organization.chapters.find((c) => c.files.some((f) => f.path === path)) ?? null;
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

  if (mode === 'risk' && org) {
    const ranks = new Map<string, number>();
    for (const chapter of org.chapters) {
      for (const file of chapter.files) ranks.set(file.path, RISK_RANK[file.risk]);
    }
    // Stable sort keeps patch order within each severity tier.
    const ranked = [...patchOrder].sort(
      (a, b) => (ranks.get(a) ?? RISK_LEVELS.length) - (ranks.get(b) ?? RISK_LEVELS.length),
    );
    return new Map(ranked.map((path, i) => [path, i]));
  }

  return new Map(patchOrder.map((path, i) => [path, i]));
}
