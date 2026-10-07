import { describe, expect, it } from 'vitest';
import { aiStatus } from './ai-requests';
import { aiRequest } from '../test/fixtures';

describe('aiStatus', () => {
  it('reports offline before anything else', () => {
    expect(aiStatus([aiRequest({ status: 'working' })], false).tone).toBe('offline');
  });

  it('puts a parked question ahead of work in flight', () => {
    const status = aiStatus([aiRequest({ status: 'working' }), aiRequest({ id: 'q', status: 'awaiting_input' })], true);
    expect(status).toEqual({ label: 'Claude has a question', detail: '', tone: 'ask' });
  });

  it('names the organize pass and its phase', () => {
    const status = aiStatus([aiRequest({ source: 'system', status: 'working', phase: 'reading files…' })], true);
    expect(status).toEqual({ label: 'Claude is organizing', detail: 'reading files', tone: 'busy' });
  });

  it('is idle once every request settled', () => {
    expect(aiStatus([aiRequest({ status: 'done' })], true).tone).toBe('idle');
  });
});
