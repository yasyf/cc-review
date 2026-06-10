import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';

export type ViewMode = 'default' | 'story' | 'risk';

interface StoredPrefs {
  viewMode: ViewMode;
  hideReviewed: boolean;
}

const storageKey = (reviewId: string) => `cc-review:view:${reviewId}`;

function readPrefs(reviewId: string): StoredPrefs {
  const raw = localStorage.getItem(storageKey(reviewId));
  return raw ? (JSON.parse(raw) as StoredPrefs) : { viewMode: 'default', hideReviewed: false };
}

interface ViewPrefsValue extends StoredPrefs {
  // Paths the user explicitly peeked at: an override both expands a collapsed
  // reviewed file and exempts it from the hide-reviewed filter. In-memory only.
  expandOverrides: ReadonlySet<string>;
  setViewMode(mode: ViewMode): void;
  setHideReviewed(hide: boolean): void;
  toggleExpandOverride(path: string): void;
  clearExpandOverride(path: string): void;
}

const ViewPrefsContext = createContext<ViewPrefsValue | null>(null);

export function useViewPrefs(): ViewPrefsValue {
  const value = useContext(ViewPrefsContext);
  if (!value) throw new Error('useViewPrefs must be used within ViewPrefsProvider');
  return value;
}

export function ViewPrefsProvider({
  reviewId,
  versionId,
  children,
}: {
  reviewId: string;
  versionId: string;
  children: ReactNode;
}) {
  const [prefs, setPrefs] = useState<StoredPrefs>(() => readPrefs(reviewId));

  useEffect(() => {
    localStorage.setItem(storageKey(reviewId), JSON.stringify(prefs));
  }, [reviewId, prefs]);

  const [expandOverrides, setExpandOverrides] = useState<ReadonlySet<string>>(new Set());

  // Peeks are per version: a new version's reviewed files start folded again.
  useEffect(() => {
    setExpandOverrides(new Set());
  }, [versionId]);

  const setViewMode = useCallback((viewMode: ViewMode) => {
    setPrefs((prev) => ({ ...prev, viewMode }));
  }, []);

  const setHideReviewed = useCallback((hideReviewed: boolean) => {
    setPrefs((prev) => ({ ...prev, hideReviewed }));
  }, []);

  const toggleExpandOverride = useCallback((path: string) => {
    setExpandOverrides((prev) => {
      const next = new Set(prev);
      if (next.has(path)) next.delete(path);
      else next.add(path);
      return next;
    });
  }, []);

  const clearExpandOverride = useCallback((path: string) => {
    setExpandOverrides((prev) => {
      if (!prev.has(path)) return prev;
      const next = new Set(prev);
      next.delete(path);
      return next;
    });
  }, []);

  return (
    <ViewPrefsContext.Provider
      value={{
        ...prefs,
        expandOverrides,
        setViewMode,
        setHideReviewed,
        toggleExpandOverride,
        clearExpandOverride,
      }}
    >
      {children}
    </ViewPrefsContext.Provider>
  );
}
