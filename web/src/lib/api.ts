import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  createQueryClient,
  request,
  scopedKey,
  useOptimisticMutation,
} from '@cc-interact/react';
import type {
  AiRequest,
  AskAnswer,
  LineRange,
  ProvenanceResponse,
  SessionResponse,
  Side,
} from './types';

export type VersionKey = number | 'latest';

export const queryClient = createQueryClient();

export const sessionKey = (slug: string, version: VersionKey) =>
  scopedKey('session', slug, version);

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

// Fetched lazily when a turn's activity section opens. The daemon caches
// closed turns; staleTime keeps the browser from re-shelling on every toggle.
export function useTurnProvenance(turnId: string, enabled: boolean) {
  return useQuery({
    queryKey: ['provenance', turnId] as const,
    queryFn: () => request<ProvenanceResponse>(`/api/turns/${turnId}/provenance`),
    enabled,
    staleTime: Infinity,
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

interface FileStatesResult {
  states: { path: string; reviewed: boolean; hidden: boolean }[];
}

export function useSetFileStates(slug: string, version?: number) {
  // Optimistic: checkbox → collapse must be instant. SSE redelivers the same
  // absolute per-path state, so the echo converges with this merge.
  return useOptimisticMutation<FileStatePatch[], FileStatesResult, SessionResponse>({
    mutationFn: (files) =>
      request<FileStatesResult>('/api/file-states', {
        method: 'POST',
        body: JSON.stringify({ reviewId: slug, files }),
      }),
    queryKey: () => sessionKey(slug, version ?? 'latest'),
    applyOptimistic: (current, files) => {
      const fileStates = { ...current.fileStates };
      for (const patch of files) {
        const prev = fileStates[patch.path] ?? { reviewed: false, hidden: false };
        fileStates[patch.path] = {
          reviewed: patch.reviewed ?? prev.reviewed,
          hidden: patch.hidden ?? prev.hidden,
        };
      }
      return { ...current, fileStates };
    },
    invalidate: () => ({ queryKey: ['session', slug], exact: false }),
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

export function useAnswerAiRequest(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: ({ id, answer, askAnswer }: { id: string; answer?: string; askAnswer?: AskAnswer }) =>
      request<{ request: AiRequest; claudeConnected: boolean }>(`/api/ai-requests/${id}/answer`, {
        method: 'POST',
        body: JSON.stringify({ answer, askAnswer }),
      }),
    // The answered request streams back as ai.request.created; invalidate as a net.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useSubmit(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      request<{ ok: boolean; feedbackPath: string }>('/api/submit', {
        method: 'POST',
        body: JSON.stringify({ reviewId: slug }),
      }),
    // The new status streams back as status.changed, but a cache keyed to an
    // older version rejects that frame; invalidate as a net.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}

export function useClose(slug: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: () =>
      request<{ ok: boolean }>('/api/close', {
        method: 'POST',
        body: JSON.stringify({ reviewId: slug }),
      }),
    // Same safety net as useSubmit.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', slug], exact: false });
    },
  });
}
