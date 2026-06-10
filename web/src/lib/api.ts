import { QueryClient, useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import type { LineRange, SessionResponse, Side } from './types';

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

export interface CreateReplyInput {
  commentId: string;
  answer?: string;
  body?: string;
  questionReplyId?: string;
}

export function useCreateReply() {
  return useMutation({
    mutationFn: ({ commentId, ...rest }: CreateReplyInput) =>
      request<{ id: string }>(`/api/replies/${commentId}`, {
        method: 'POST',
        body: JSON.stringify({ origin: 'user', ...rest }),
      }),
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
