import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';

export type ViewMode = 'default' | 'story' | 'todo';

interface StoredPrefs {
  viewMode: ViewMode;
  hideReviewed: boolean;
  focusMode: boolean;
}

const storageKey = (reviewId: string) => `cc-review:view:${reviewId}`;

function sanitizeViewMode(stored: unknown): ViewMode {
  return stored === 'story' || stored === 'todo' ? stored : 'default';
}

function readPrefs(reviewId: string): StoredPrefs {
  const raw = localStorage.getItem(storageKey(reviewId));
  if (!raw) return { viewMode: 'default', hideReviewed: false, focusMode: true };
  const stored = JSON.parse(raw) as {
    viewMode?: unknown;
    hideReviewed?: unknown;
    focusMode?: unknown;
  };
  return {
    viewMode: sanitizeViewMode(stored.viewMode),
    hideReviewed: stored.hideReviewed === true,
    // Default-on: only an explicit stored `false` disables focus mode.
    focusMode: stored.focusMode !== false,
  };
}

interface ViewPrefsValue extends StoredPrefs {
  // itemIds the user explicitly peeked at: an override both expands a collapsed
  // reviewed file and exempts it from the hide-reviewed filter. In-memory only.
  expandOverrides: ReadonlySet<string>;
  // The turn whose attributed lines are highlighted in the diff; everything
  // else dims. In-memory only.
  activeTurnId: string | null;
  setViewMode(mode: ViewMode): void;
  setHideReviewed(hide: boolean): void;
  setFocusMode(focus: boolean): void;
  toggleExpandOverride(id: string): void;
  clearExpandOverride(id: string): void;
  setActiveTurnId(id: string | null): void;
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
  const [activeTurnId, setActiveTurnId] = useState<string | null>(null);

  // Peeks and turn focus are per version: a new version's reviewed files start
  // folded again and its turns are a different set.
  useEffect(() => {
    setExpandOverrides(new Set());
    setActiveTurnId(null);
  }, [versionId]);

  const setViewMode = useCallback((viewMode: ViewMode) => {
    setPrefs((prev) => ({ ...prev, viewMode }));
  }, []);

  const setHideReviewed = useCallback((hideReviewed: boolean) => {
    setPrefs((prev) => ({ ...prev, hideReviewed }));
  }, []);

  const setFocusMode = useCallback((focusMode: boolean) => {
    setPrefs((prev) => ({ ...prev, focusMode }));
  }, []);

  const toggleExpandOverride = useCallback((id: string) => {
    setExpandOverrides((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  const clearExpandOverride = useCallback((id: string) => {
    setExpandOverrides((prev) => {
      if (!prev.has(id)) return prev;
      const next = new Set(prev);
      next.delete(id);
      return next;
    });
  }, []);

  return (
    <ViewPrefsContext.Provider
      value={{
        ...prefs,
        expandOverrides,
        activeTurnId,
        setViewMode,
        setHideReviewed,
        setFocusMode,
        toggleExpandOverride,
        clearExpandOverride,
        setActiveTurnId,
      }}
    >
      {children}
    </ViewPrefsContext.Provider>
  );
}
