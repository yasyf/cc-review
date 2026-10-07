import { createContext, useCallback, useContext, useEffect, useLayoutEffect, useRef, useState, useSyncExternalStore } from 'react';
import type { ReactNode } from 'react';

export type SidebarTab = 'files' | 'comments' | 'activity';

export type SidebarMode = 'docked' | 'hidden' | 'overlay';

export const SIDEBAR_MIN_WIDTH_PX = 220;
export const SIDEBAR_MAX_WIDTH_PX = 560;
export const SIDEBAR_DEFAULT_WIDTH_PX = 288;

const WIDTH_KEY = 'cc-review:sidebar-width';
const COLLAPSED_KEY = 'cc-review:sidebar-collapsed';
const NARROW_QUERY = '(max-width: 900px)';

export function parseSidebarTab(raw: unknown): SidebarTab | undefined {
  return raw === 'comments' || raw === 'activity' ? raw : undefined;
}

export function clampSidebarWidth(width: number): number {
  return Math.round(Math.min(SIDEBAR_MAX_WIDTH_PX, Math.max(SIDEBAR_MIN_WIDTH_PX, width)));
}

function readWidth(): number {
  const stored = Number(localStorage.getItem(WIDTH_KEY));
  return stored > 0 ? clampSidebarWidth(stored) : SIDEBAR_DEFAULT_WIDTH_PX;
}

function subscribeNarrow(onChange: () => void): () => void {
  const query = window.matchMedia(NARROW_QUERY);
  query.addEventListener('change', onChange);
  return () => query.removeEventListener('change', onChange);
}

function isNarrow(): boolean {
  return window.matchMedia(NARROW_QUERY).matches;
}

interface SidebarLayout {
  mode: SidebarMode;
  width: number;
  setWidth(width: number): void;
  toggle(): void;
  dismissOverlay(): void;
}

const SidebarLayoutContext = createContext<SidebarLayout | null>(null);

export function useSidebarLayout(): SidebarLayout {
  const value = useContext(SidebarLayoutContext);
  if (!value) throw new Error('useSidebarLayout must be used within SidebarFrame');
  return value;
}

export function SidebarFrame({ children }: { children: ReactNode }) {
  const narrow = useSyncExternalStore(subscribeNarrow, isNarrow);
  const [collapsed, setCollapsed] = useState(() => localStorage.getItem(COLLAPSED_KEY) === 'true');
  const [overlayOpen, setOverlayOpen] = useState(false);
  const [width, setWidthState] = useState(readWidth);
  const ref = useRef<HTMLDivElement>(null);

  const mode: SidebarMode = narrow ? (overlayOpen ? 'overlay' : 'hidden') : collapsed ? 'hidden' : 'docked';

  useLayoutEffect(() => {
    ref.current?.style.setProperty('--sidebar-w', `${width}px`);
  }, [width]);

  useEffect(() => {
    if (!narrow) setOverlayOpen(false);
  }, [narrow]);

  useEffect(() => {
    if (!overlayOpen) return;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') setOverlayOpen(false);
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [overlayOpen]);

  const setWidth = useCallback((next: number) => {
    const clamped = clampSidebarWidth(next);
    localStorage.setItem(WIDTH_KEY, String(clamped));
    setWidthState(clamped);
  }, []);

  const toggle = useCallback(() => {
    if (narrow) {
      setOverlayOpen((open) => !open);
      return;
    }
    setCollapsed((prev) => {
      localStorage.setItem(COLLAPSED_KEY, String(!prev));
      return !prev;
    });
  }, [narrow]);

  const dismissOverlay = useCallback(() => setOverlayOpen(false), []);

  return (
    <SidebarLayoutContext.Provider value={{ mode, width, setWidth, toggle, dismissOverlay }}>
      <div ref={ref} className="review-root" data-sidebar={mode}>
        {children}
        {mode === 'overlay' ? <div className="sidebar-scrim" onClick={dismissOverlay} /> : null}
      </div>
    </SidebarLayoutContext.Provider>
  );
}
