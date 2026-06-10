import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { AiRequest, AskAnswer, LineRange, SessionResponse, Side } from './types';

export type VersionKey = number | 'latest';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 5_000, retry: 1, refetchOnWindowFocus: false },
  },
});

export const sessionKey = (slug: string, version: VersionKey) =>
  ['session', slug, version] as const;

async function request<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(path, {
    ...init,
    headers: { 'content-type': 'application/json', ...init?.headers },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${init?.method ?? 'GET'} ${path} failed (${res.status}): ${text}`);
  }
  return res.json() as Promise<T>;
}

export function fetchSession(slug: string, version?: number): Promise<SessionResponse> {
  const path =
    version === undefined ? `/api/session/${slug}` : `/api/session/${slug}?version=${version}`;
  return request<SessionResponse>(path);
}

export function useSession(slug: string, version?: number) {
  return useQuery({
    queryKey: sessionKey(slug, version ?? 'latest'),
    queryFn: () => fetchSession(slug, version),
  });
}

export interface CreateCommentInput {
  versionId: string;
  filePath: string;
  side: Side;
  range: LineRange;
  lineContent: string;
  body: string;
}

export function useCreateComment(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateCommentInput) =>
      request<{ id: string }>('/api/comments', {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    // The created comment streams back over SSE and patches the session cache;
    // we only invalidate as a safety net if the stream is momentarily behind.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useResolveComment() {
  return useMutation({
    mutationFn: (input: { id: string; status: 'open' | 'resolved'; body?: string }) =>
      request<{ ok: true }>(`/api/comments/${input.id}`, {
        method: 'PUT',
        body: JSON.stringify({ status: input.status, body: input.body }),
      }),
  });
}

// A reply is exactly one of: a free-text note, or a structured answer to an
// ask reply. Plain question replies are answered post-submit via the drain,
// never from the web.
export type CreateReplyInput =
  | { commentId: string; body: string }
  | { commentId: string; askAnswer: AskAnswer; questionReplyId: string };

export function useCreateReply() {
  return useMutation({
    // Notes return {id}; ask answers return {ok} — no caller reads either.
    mutationFn: ({ commentId, ...rest }: CreateReplyInput) =>
      request<{ id: string } | { ok: true }>(`/api/replies/${commentId}`, {
        method: 'POST',
        body: JSON.stringify({ origin: 'user', ...rest }),
      }),
  });
}

export interface FileStatePatch {
  path: string;
  reviewed?: boolean;
  hidden?: boolean;
}

export function useSetFileStates(slug: string, version?: number) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (files: FileStatePatch[]) =>
      request<{ states: { path: string; reviewed: boolean; hidden: boolean }[] }>(
        '/api/file-states',
        { method: 'POST', body: JSON.stringify({ reviewId: slug, files }) },
      ),
    // Optimistic: checkbox → collapse must be instant. SSE redelivers the same
    // absolute per-path state, so the echo converges with this merge.
    onMutate: async (files) => {
      const key = sessionKey(slug, version ?? 'latest');
      // An in-flight session refetch (kicked off by another mutation's
      // invalidate) would resolve with a pre-mutation snapshot and clobber
      // both this merge and the SSE echo.
      await qc.cancelQueries({ queryKey: key });
      const current = qc.getQueryData<SessionResponse>(key);
      if (!current) return;
      const fileStates = { ...current.fileStates };
      for (const patch of files) {
        const prev = fileStates[patch.path] ?? { reviewed: false, hidden: false };
        fileStates[patch.path] = {
          reviewed: patch.reviewed ?? prev.reviewed,
          hidden: patch.hidden ?? prev.hidden,
        };
      }
      qc.setQueryData<SessionResponse>(key, { ...current, fileStates });
    },
    onError: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useCreateAiRequest(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (prompt: string) =>
      request<{ request: AiRequest; claudeConnected: boolean }>('/api/ai-requests', {
        method: 'POST',
        body: JSON.stringify({ reviewId: slug, prompt }),
      }),
    // The created request streams back over SSE; invalidate as a safety net.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useUndoAiRequest(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (id: string) =>
      request<{ ok: true }>(`/api/ai-requests/${id}/undo`, { method: 'POST' }),
    // Undo emits file.states + ai.request.updated over SSE; same safety net.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useSubmit(slug: string) {
  return useMutation({
    mutationFn: () =>
      request<{ ok: boolean; feedbackPath: string }>('/api/submit', {
        method: 'POST',
        body: JSON.stringify({ reviewId: slug }),
      }),
  });
}
