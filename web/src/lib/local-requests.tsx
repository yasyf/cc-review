import { createContext, useCallback, useContext, useEffect, useState } from 'react';
import type { ReactNode } from 'react';
import type { FileRef } from './diff';

// A ⚡ instant-lane edit: file-state changes the reviewer applied client-side,
// bypassing the agent. `prior` is snapshotted at click time (keyed by itemId) so
// undo restores the exact pre-edit state without a server round-trip. Shaped to
// render alongside server AiRequests in the deck's result stream.
export interface LocalRequest {
  id: string;
  kind: 'local';
  label: string;
  refs: FileRef[];
  prior: Record<string, { reviewed: boolean; hidden: boolean }>;
  createdAt: string;
}

interface LocalRequestsValue {
  requests: LocalRequest[];
  add(label: string, refs: FileRef[], prior: LocalRequest['prior']): LocalRequest;
  remove(id: string): void;
}

const LocalRequestsContext = createContext<LocalRequestsValue | null>(null);

export function useLocalRequests(): LocalRequestsValue {
  const value = useContext(LocalRequestsContext);
  if (!value) throw new Error('useLocalRequests must be used within LocalRequestsProvider');
  return value;
}

let seq = 0;

export function LocalRequestsProvider({
  versionId,
  children,
}: {
  versionId: string;
  children: ReactNode;
}) {
  const [requests, setRequests] = useState<LocalRequest[]>([]);

  // Instant rows reference this version's paths and priors; a new version starts
  // fresh (the old paths and their undo no longer apply).
  useEffect(() => setRequests([]), [versionId]);

  const add = useCallback((label: string, refs: FileRef[], prior: LocalRequest['prior']) => {
    const req: LocalRequest = {
      id: `local:${++seq}`,
      kind: 'local',
      label,
      refs,
      prior,
      createdAt: new Date().toISOString(),
    };
    setRequests((prev) => [req, ...prev]);
    return req;
  }, []);

  const remove = useCallback((id: string) => {
    setRequests((prev) => prev.filter((r) => r.id !== id));
  }, []);

  return (
    <LocalRequestsContext.Provider value={{ requests, add, remove }}>
      {children}
    </LocalRequestsContext.Provider>
  );
}
