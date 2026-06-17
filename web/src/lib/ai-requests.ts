import type { LocalRequest } from './local-requests';
import type { AiRequest, AiRequestStatus } from './types';

const inFlight = (r: AiRequest) => r.status === 'pending' || r.status === 'working';

export function isOrganizing(requests: AiRequest[]): boolean {
  return requests.some((r) => r.source === 'system' && inFlight(r));
}

export function userRequestInFlight(requests: AiRequest[]): boolean {
  return requests.some((r) => r.source === 'user' && inFlight(r));
}

// Active = in motion or awaiting the reviewer: these stay pinned in the deck so
// a working request or a parked question is never scrolled out of sight.
const ACTIVE_STATUSES: ReadonlySet<AiRequestStatus> = new Set([
  'pending',
  'working',
  'awaiting_input',
  'answered',
]);

export function isActive(request: AiRequest): boolean {
  return ACTIVE_STATUSES.has(request.status);
}

// Distinct prior user prompts, newest first — replayable from the command menu.
export function recentUserCommands(requests: AiRequest[], limit = 6): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const r of requests) {
    if (r.source !== 'user') continue;
    const prompt = r.prompt.trim();
    if (prompt === '' || seen.has(prompt)) continue;
    seen.add(prompt);
    out.push(prompt);
    if (out.length >= limit) break;
  }
  return out;
}

// One row of the deck's result stream: a server AI request or a client ⚡ edit.
export type ResultItem =
  | { kind: 'ai'; request: AiRequest }
  | { kind: 'local'; request: LocalRequest };

// Server requests + local ⚡ edits, newest first. Both carry ISO createdAt, so a
// lexical compare orders them correctly.
export function resultStream(ai: AiRequest[], local: LocalRequest[]): ResultItem[] {
  const items: ResultItem[] = [
    ...ai.map((request) => ({ kind: 'ai' as const, request })),
    ...local.map((request) => ({ kind: 'local' as const, request })),
  ];
  items.sort((a, b) => b.request.createdAt.localeCompare(a.request.createdAt));
  return items;
}
