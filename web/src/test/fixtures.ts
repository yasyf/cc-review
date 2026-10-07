import type { AiRequest, ChapterFile, Comment, Reply, Section, SessionResponse } from '../lib/types';

export function section(overrides: Partial<Section> = {}): Section {
  return {
    sectionId: 'sec-1',
    position: 0,
    sectionKey: '',
    branch: 'feat/x',
    parentBranch: 'main',
    baseRef: 'base',
    headRef: 'head',
    pending: true,
    patchText: '',
    files: [],
    fileStates: {},
    organization: null,
    ...overrides,
  };
}

export function comment(overrides: Partial<Comment> = {}): Comment {
  return {
    id: 'c1',
    versionId: 'v1',
    sectionId: 'sec-1',
    branch: 'feat/x',
    pending: true,
    filePath: 'a.ts',
    side: 'additions',
    range: { start: 1, end: 1 },
    lineContent: 'const a = 1;',
    body: 'why?',
    origin: 'user',
    status: 'open',
    createdAt: '2026-10-01T00:00:00Z',
    replies: [],
    ...overrides,
  };
}

export function reply(overrides: Partial<Exclude<Reply, { kind: 'ask' }>> = {}): Reply {
  return {
    id: 'r1',
    commentId: 'c1',
    origin: 'claude',
    kind: 'clarification',
    body: 'because',
    createdAt: '2026-10-01T00:00:00Z',
    ...overrides,
  };
}

export function aiRequest(overrides: Partial<AiRequest> = {}): AiRequest {
  return {
    id: 'ai1',
    source: 'user',
    prompt: 'hide tests',
    status: 'pending',
    summary: '',
    unmatched: [],
    changes: [],
    createdAt: '2026-10-01T00:00:00Z',
    updatedAt: '2026-10-01T00:00:00Z',
    ...overrides,
  };
}

export function chapterFile(path: string, risk: ChapterFile['risk']): ChapterFile {
  return { path, risk, rationale: '', focus: '', lines: [] };
}

export function session(overrides: Partial<SessionResponse> = {}): SessionResponse {
  return {
    review: { id: 'rev', status: 'open', repoRoot: '/repo', branch: 'feat/x', createdAt: '2026-10-01T00:00:00Z' },
    version: 1,
    versionId: 'v1',
    sections: [section()],
    comments: [],
    annotations: [],
    aiRequests: [],
    turns: [],
    turnActivity: {},
    claudeConnected: false,
    latestEventSeq: '0',
    ...overrides,
  };
}
