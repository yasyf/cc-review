import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { LineRange, SessionResponse, Side, VersionSummary } from './types';

export type VersionKey = number | 'latest';

export const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 5_000, retry: 1, refetchOnWindowFocus: false },
  },
});

export const sessionKey = (reviewId: string, version: VersionKey) =>
  ['session', reviewId, version] as const;

export const versionsKey = (reviewId: string) => ['versions', reviewId] as const;

export const notificationsKey = (reviewId: string) => ['notifications', reviewId] as const;

function withToken(path: string, token: string): string {
  const sep = path.includes('?') ? '&' : '?';
  return `${path}${sep}t=${encodeURIComponent(token)}`;
}

async function request<T>(path: string, token: string, init?: RequestInit): Promise<T> {
  const res = await fetch(withToken(path, token), {
    ...init,
    headers: { 'content-type': 'application/json', ...init?.headers },
  });
  if (!res.ok) {
    const text = await res.text();
    throw new Error(`${init?.method ?? 'GET'} ${path} failed (${res.status}): ${text}`);
  }
  return res.json() as Promise<T>;
}

export function fetchSession(
  reviewId: string,
  token: string,
  version: VersionKey,
): Promise<SessionResponse> {
  const path =
    version === 'latest'
      ? `/api/session/${reviewId}`
      : `/api/session/${reviewId}?version=${version}`;
  return request<SessionResponse>(path, token);
}

export function useSession(reviewId: string, token: string, version: VersionKey) {
  return useQuery({
    queryKey: sessionKey(reviewId, version),
    queryFn: () => fetchSession(reviewId, token, version),
  });
}

export function useVersions(reviewId: string, token: string) {
  return useQuery({
    queryKey: versionsKey(reviewId),
    queryFn: () => request<VersionSummary[]>(`/api/session/${reviewId}/versions`, token),
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

export function useCreateComment(reviewId: string, token: string) {
  const qc = useQueryClient();
  return useMutation({
    mutationFn: (input: CreateCommentInput) =>
      request<{ id: string }>('/api/comments', token, {
        method: 'POST',
        body: JSON.stringify(input),
      }),
    // The created comment streams back over SSE and patches the session cache;
    // we only invalidate as a safety net if the stream is momentarily behind.
    onSuccess: () => {
      void qc.invalidateQueries({ queryKey: ['session', reviewId], exact: false });
    },
  });
}

export function useResolveComment(token: string) {
  return useMutation({
    mutationFn: (input: { id: string; status: 'open' | 'resolved'; body?: string }) =>
      request<{ ok: true }>(`/api/comments/${input.id}`, token, {
        method: 'PUT',
        body: JSON.stringify({ status: input.status, body: input.body }),
      }),
  });
}

export interface CreateReplyInput {
  commentId: string;
  answer?: string;
  body?: string;
  questionReplyId?: string;
}

export function useCreateReply(token: string) {
  return useMutation({
    mutationFn: ({ commentId, ...rest }: CreateReplyInput) =>
      request<{ id: string }>(`/api/replies/${commentId}`, token, {
        method: 'POST',
        body: JSON.stringify({ origin: 'user', ...rest }),
      }),
  });
}

export function useSubmit(reviewId: string, token: string) {
  return useMutation({
    mutationFn: () =>
      request<{ ok: boolean; feedbackPath: string }>('/api/submit', token, {
        method: 'POST',
        body: JSON.stringify({ reviewId }),
      }),
  });
}
