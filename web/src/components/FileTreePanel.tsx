import { useEffect, useMemo, useRef } from 'react';
import type { GitStatus } from '@pierre/trees';
import { FileTree, useFileTree } from '@pierre/trees/react';
import { useSetFileStates } from '../lib/api';
import type { FileRef } from '../lib/diff/items';
import { riskOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { FileMeta, FileState, Organization, Section, SessionResponse } from '../lib/types';
import { HiddenFilesStrip } from './HiddenFilesStrip';
import { iconSprite, iconSymbolId } from './ui/icons';

// git name-status codes as the daemon emits them (gitdiff truncates scored
// codes like R100 to their letter).
const STATUS_MAP: Record<string, GitStatus> = {
  A: 'added',
  M: 'modified',
  D: 'deleted',
  R: 'renamed',
  C: 'added',
  T: 'modified',
};

interface TreeStateSnapshot {
  fileStates: Record<string, FileState>;
  organization: Organization | null;
}

// The virtualized tree needs a bounded host height; stacked sections size to
// content (over-estimated from files + dir nodes) so the panel scrolls.
const ITEM_HEIGHT_PX = 30;

const TREE_ICONS = { set: 'complete', spriteSheet: iconSprite(['check', 'alert']) } as const;

function estimateTreeHeight(paths: string[]): number {
  const dirs = new Set<string>();
  for (const path of paths) {
    const parts = path.split('/');
    for (let i = 1; i < parts.length; i++) dirs.add(parts.slice(0, i).join('/'));
  }
  return (dirs.size + paths.length) * ITEM_HEIGHT_PX + ITEM_HEIGHT_PX;
}

// useFileTree captures its options at construction and never re-reads them, so
// the parent remounts this component (keyed by the visible path set) whenever
// membership changes. Decoration changes (reviewed/risk) are far more frequent
// and must NOT remount: renderRowDecoration reads live state from a ref, and a
// setGitStatus call repaints every row in place, keeping scroll + expansion.
function Tree({
  sectionKey,
  files,
  fileStates,
  organization,
  height,
  onSelectFile,
}: {
  sectionKey: string;
  files: FileMeta[];
  fileStates: Record<string, FileState>;
  organization: Organization | null;
  height: string;
  onSelectFile(ref: FileRef): void;
}) {
  const { slug, version } = useReview();
  const { mutate: mutateStates } = useSetFileStates(slug, version);

  const onSelectFileRef = useRef(onSelectFile);
  useEffect(() => {
    onSelectFileRef.current = onSelectFile;
  }, [onSelectFile]);

  const stateRef = useRef<TreeStateSnapshot>({ fileStates, organization });

  const filePaths = useMemo(() => new Set(files.map((f) => f.path)), [files]);
  const gitStatus = useMemo(
    () => files.map((f) => ({ path: f.path, status: STATUS_MAP[f.status] ?? 'modified' })),
    [files],
  );

  // @pierre/trees exposes no per-row class hook, but every row carries a stable
  // data-item-path and accepts unsafeCSS into its shadow root — so reviewed rows
  // dim + strike through (matching .chapter-row-reviewed). unsafeCSS is captured
  // at construction, so the parent remounts this component when the reviewed set
  // changes (see the key below).
  const dimCSS = useMemo(() => {
    const reviewed = files.filter((f) => fileStates[f.path]?.reviewed);
    if (reviewed.length === 0) return '';
    const selectors = reviewed
      .map((f) => `[data-item-path="${f.path.replace(/\\/g, '\\\\').replace(/"/g, '\\"')}"]`)
      .join(',');
    return `${selectors}{opacity:0.5;text-decoration:line-through;}`;
  }, [files, fileStates]);

  const { model } = useFileTree({
    paths: files.map((f) => f.path),
    initialExpansion: 'open',
    flattenEmptyDirectories: true,
    gitStatus,
    icons: TREE_ICONS,
    unsafeCSS: dimCSS,
    composition: { contextMenu: { enabled: true, triggerMode: 'both' } },
    // Decorations are non-interactive spans; interactive mark/hide lives in
    // the context menu below.
    renderRowDecoration: ({ item }) => {
      if (item.kind !== 'file') return null;
      const snapshot = stateRef.current;
      if (snapshot.fileStates[item.path]?.reviewed) return { icon: iconSymbolId('check'), title: 'Reviewed' };
      if (riskOf(snapshot.organization, item.path) === 'high') {
        return { icon: iconSymbolId('alert'), title: 'High risk' };
      }
      return null;
    },
    onSelectionChange: (selectedPaths) => {
      const path = selectedPaths[0];
      if (path && filePaths.has(path)) {
        onSelectFileRef.current({ sectionKey, path });
        // The tree dedupes selection (re-clicking the selected row never
        // re-fires this), so deselect after navigating to make every click a
        // fresh selection change. Deferred to avoid re-entering the
        // controller's listener iteration; the resulting empty-selection
        // emission fails the guard above, so the cycle stops after one bounce.
        queueMicrotask(() => model.getItem(path)?.deselect());
      }
    },
  });

  useEffect(() => {
    stateRef.current = { fileStates, organization };
    // Repaints every row so decorations re-read the ref without a remount.
    model.setGitStatus(gitStatus);
  }, [model, fileStates, organization, gitStatus]);

  return (
    <FileTree
      model={model}
      style={{ height }}
      renderContextMenu={(item, context) => {
        if (item.kind !== 'file') return null;
        const reviewed = fileStates[item.path]?.reviewed ?? false;
        return (
          <div className="tree-menu">
            <button
              type="button"
              onClick={() => {
                mutateStates([{ sectionKey, path: item.path, reviewed: !reviewed }]);
                context.close();
              }}
            >
              {reviewed ? 'Mark not viewed' : 'Mark viewed'}
            </button>
            <button
              type="button"
              onClick={() => {
                mutateStates([{ sectionKey, path: item.path, hidden: true }]);
                context.close();
              }}
            >
              Hide file
            </button>
          </div>
        );
      }}
    />
  );
}

function treeKey(section: Section, visible: FileMeta[]): string {
  return visible.map((f) => `${f.path} ${section.fileStates[f.path]?.reviewed ? 1 : 0}`).join('\n');
}

// A stacked section: a branch head above a content-sized tree.
function SectionTree({
  section,
  onSelectFile,
}: {
  section: Section;
  onSelectFile(ref: FileRef): void;
}) {
  const visible = section.files.filter((f) => !section.fileStates[f.path]?.hidden);
  if (visible.length === 0) return null;
  const reviewed = visible.filter((f) => section.fileStates[f.path]?.reviewed).length;

  return (
    <div className="section-group">
      <header className="section-group-head">
        <span className="section-group-title">
          {section.pending ? 'Working tree' : section.branch}
        </span>
        <span className="chapter-progress">
          {reviewed}/{visible.length}
        </span>
      </header>
      <Tree
        key={treeKey(section, visible)}
        sectionKey={section.sectionKey}
        files={visible}
        fileStates={section.fileStates}
        organization={section.organization}
        height={`${estimateTreeHeight(visible.map((f) => f.path))}px`}
        onSelectFile={onSelectFile}
      />
    </div>
  );
}

export function FileTreePanel({
  session,
  onSelectFile,
}: {
  session: SessionResponse;
  onSelectFile(ref: FileRef): void;
}) {
  const hidden: FileRef[] = session.sections.flatMap((s) =>
    s.files
      .filter((f) => s.fileStates[f.path]?.hidden)
      .map((f) => ({ sectionKey: s.sectionKey, path: f.path })),
  );

  // A flat review renders exactly one section's tree filling the panel, with no
  // section head — pixel-identical to the single-diff tree.
  const flat = session.sections.length === 1 ? session.sections[0] : null;
  const flatVisible = flat ? flat.files.filter((f) => !flat.fileStates[f.path]?.hidden) : [];

  return (
    <>
      <div className="sidebar-tree">
        {flat ? (
          <Tree
            key={treeKey(flat, flatVisible)}
            sectionKey={flat.sectionKey}
            files={flatVisible}
            fileStates={flat.fileStates}
            organization={flat.organization}
            height="100%"
            onSelectFile={onSelectFile}
          />
        ) : (
          session.sections.map((section) => (
            <SectionTree key={section.sectionKey} section={section} onSelectFile={onSelectFile} />
          ))
        )}
      </div>
      <HiddenFilesStrip files={hidden} />
    </>
  );
}
