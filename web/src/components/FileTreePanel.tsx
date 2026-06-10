import { useEffect, useMemo, useRef } from 'react';
import type { GitStatus } from '@pierre/trees';
import { FileTree, useFileTree } from '@pierre/trees/react';
import { useSetFileStates } from '../lib/api';
import { riskOf } from '../lib/order';
import { useReview } from '../lib/review-context';
import type { FileMeta, FileState, Organization } from '../lib/types';
import { HiddenFilesStrip } from './HiddenFilesStrip';

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

// useFileTree captures its options at construction and never re-reads them, so
// the parent remounts this component (keyed by the visible path set) whenever
// membership changes. Decoration changes (reviewed/risk) are far more frequent
// and must NOT remount: renderRowDecoration reads live state from a ref, and a
// setGitStatus call repaints every row in place, keeping scroll + expansion.
function Tree({
  files,
  fileStates,
  organization,
  onSelectFile,
}: {
  files: FileMeta[];
  fileStates: Record<string, FileState>;
  organization: Organization | null;
  onSelectFile(path: string): void;
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

  const { model } = useFileTree({
    paths: files.map((f) => f.path),
    initialExpansion: 'open',
    flattenEmptyDirectories: true,
    gitStatus,
    composition: { contextMenu: { enabled: true, triggerMode: 'both' } },
    // Decorations are non-interactive spans; interactive mark/hide lives in
    // the context menu below.
    renderRowDecoration: ({ item }) => {
      if (item.kind !== 'file') return null;
      const snapshot = stateRef.current;
      if (snapshot.fileStates[item.path]?.reviewed) return { text: '✓', title: 'Reviewed' };
      if (riskOf(snapshot.organization, item.path) === 'high') {
        return { text: '!', title: 'High risk' };
      }
      return null;
    },
    onSelectionChange: (selectedPaths) => {
      const path = selectedPaths[0];
      if (path && filePaths.has(path)) {
        onSelectFileRef.current(path);
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
      style={{ height: '100%' }}
      renderContextMenu={(item, context) => {
        if (item.kind !== 'file') return null;
        const reviewed = fileStates[item.path]?.reviewed ?? false;
        return (
          <div className="tree-menu">
            <button
              type="button"
              onClick={() => {
                mutateStates([{ path: item.path, reviewed: !reviewed }]);
                context.close();
              }}
            >
              {reviewed ? 'Mark not viewed' : 'Mark viewed'}
            </button>
            <button
              type="button"
              onClick={() => {
                mutateStates([{ path: item.path, hidden: true }]);
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

export function FileTreePanel({
  files,
  fileStates,
  organization,
  onSelectFile,
}: {
  files: FileMeta[];
  fileStates: Record<string, FileState>;
  organization: Organization | null;
  onSelectFile(path: string): void;
}) {
  const visible = files.filter((f) => !fileStates[f.path]?.hidden);
  const hidden = files.filter((f) => fileStates[f.path]?.hidden);

  return (
    <>
      <div className="sidebar-tree">
        <Tree
          key={visible.map((f) => f.path).join('\n')}
          files={visible}
          fileStates={fileStates}
          organization={organization}
          onSelectFile={onSelectFile}
        />
      </div>
      <HiddenFilesStrip files={hidden} />
    </>
  );
}
