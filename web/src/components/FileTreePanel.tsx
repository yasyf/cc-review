import { useEffect, useMemo, useRef } from 'react';
import type { GitStatus } from '@pierre/trees';
import { FileTree, useFileTree } from '@pierre/trees/react';
import type { FileMeta } from '../lib/types';

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

// Sole owner of @pierre/trees. useFileTree captures its options at
// construction and never re-reads them, so the parent remounts this component
// (key={versionId}) whenever the file list changes.
export function FileTreePanel({
  files,
  onSelectFile,
}: {
  files: FileMeta[];
  onSelectFile(path: string): void;
}) {
  const onSelectFileRef = useRef(onSelectFile);
  useEffect(() => {
    onSelectFileRef.current = onSelectFile;
  }, [onSelectFile]);

  const filePaths = useMemo(() => new Set(files.map((f) => f.path)), [files]);

  const { model } = useFileTree({
    paths: files.map((f) => f.path),
    initialExpansion: 'open',
    flattenEmptyDirectories: true,
    gitStatus: files.map((f) => ({ path: f.path, status: STATUS_MAP[f.status] ?? 'modified' })),
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

  return (
    <div className="sidebar-tree">
      <FileTree model={model} style={{ height: '100%' }} />
    </div>
  );
}
