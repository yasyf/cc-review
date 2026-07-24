// File ordering across the diff and the sidebar panels, section-major:
// per-section mode-orders concatenated in position order, keyed by itemId.

import { fileItemId } from './diff';
import type { ChapterFile, Organization, Risk, Section, SessionResponse } from './types';
import type { ViewMode } from './view-prefs';

const RISK_RANK: Record<Risk, number> = { high: 0, medium: 1, low: 2, mechanical: 3 };

export interface TodoGroup {
  title: string;
  files: ChapterFile[];
}

// A todo group carrying the section its files belong to, for the global Todo
// checklist that spans every section.
export interface SectionTodoGroup {
  title: string;
  branch: string;
  sectionKey: string;
  files: ChapterFile[];
}

export function chapterFileOf(
  organization: Organization | null,
  path: string,
): ChapterFile | null {
  if (!organization) return null;
  for (const chapter of organization.chapters) {
    const file = chapter.files.find((f) => f.path === path);
    if (file) return file;
  }
  return null;
}

export function riskOf(organization: Organization | null, path: string): Risk | null {
  return chapterFileOf(organization, path)?.risk ?? null;
}

interface RankedGroup {
  title: string;
  files: ChapterFile[];
  index: number;
  best: number;
  high: number;
  medium: number;
}

// Chapters ranked scariest-first (best risk, then high/medium counts, then
// submitted order); paths deduped across chapters (first wins), kept to `present`.
function rankedGroups(organization: Organization | null, present: ReadonlySet<string>): RankedGroup[] {
  if (!organization) return [];
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
  return ranked;
}

export function todoGroups(organization: Organization, present: ReadonlySet<string>): TodoGroup[] {
  return rankedGroups(organization, present).map(({ title, files }) => ({ title, files }));
}

// The global scariest-first Todo checklist across every section: each section's
// ranked groups tagged with the owning branch, then globally re-ranked (section
// position breaks ties, keeping a stack trunk-most-first within a risk tier).
export function sectionTodoGroups(sections: readonly Section[]): SectionTodoGroup[] {
  const all = sections.flatMap((section, si) => {
    const present = new Set(section.files.map((f) => f.path));
    return rankedGroups(section.organization, present).map((g) => ({
      ...g,
      branch: section.branch,
      sectionKey: section.sectionKey,
      order: si * 100000 + g.index,
    }));
  });
  all.sort((a, b) => a.best - b.best || b.high - a.high || b.medium - a.medium || a.order - b.order);
  return all.map(({ title, files, branch, sectionKey }) => ({ title, files, branch, sectionKey }));
}

// Ordered unique paths within one section for the mode; unorganized files trail
// in patch order, and without an org every mode is patch order.
function sectionPathOrder(section: Section, mode: ViewMode): string[] {
  const patchOrder = section.files.map((f) => f.path);
  const org = section.organization;

  if (mode === 'story' && org) {
    const ordered = org.chapters.flatMap((c) => c.files.map((f) => f.path));
    const inChapters = new Set(ordered);
    return [...ordered, ...patchOrder.filter((path) => !inChapters.has(path))];
  }

  if (mode === 'todo' && org) {
    const ordered = todoGroups(org, new Set(patchOrder)).flatMap((g) => g.files.map((f) => f.path));
    const organized = new Set(ordered);
    return [...ordered, ...patchOrder.filter((path) => !organized.has(path))];
  }

  return patchOrder;
}

// itemId → rank for the active view mode, section-major.
export function fileOrder(session: SessionResponse, mode: ViewMode): Map<string, number> {
  const order = new Map<string, number>();
  let rank = 0;
  for (const section of session.sections) {
    for (const path of sectionPathOrder(section, mode)) {
      const id = fileItemId(section.sectionKey, path);
      if (order.has(id)) continue;
      order.set(id, rank++);
    }
  }
  return order;
}
