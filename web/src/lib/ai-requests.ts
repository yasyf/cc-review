import type { AiRequest } from './types';

const inFlight = (r: AiRequest) => r.status === 'pending' || r.status === 'working';

export function isOrganizing(requests: AiRequest[]): boolean {
  return requests.some((r) => r.source === 'system' && inFlight(r));
}

export function userRequestInFlight(requests: AiRequest[]): boolean {
  return requests.some((r) => r.source === 'user' && inFlight(r));
}
