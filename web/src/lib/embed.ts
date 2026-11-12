import { useEffect, useRef } from 'react';
import type { RefObject } from 'react';
import { useCreateAiRequest, useCreateComment } from './api';
import type { CreateCommentInput } from './api';
import { lineContentAt, parseFiles, rangeInOneHunk } from './diff/items';
import type { DiffViewHandle } from './diff/useDiffHandle';
import { useReview } from './review-context';
import type { Section, SessionResponse } from './types';

export interface EmbedSay {
  id: string;
  text: string;
  path?: string;
  line?: number;
  pr?: number;
}

export type EmbedMessage =
  | { type: 'cc-review:show'; path: string; pr?: number }
  | ({ type: 'cc-review:say' } & EmbedSay);

export type EmbedAction =
  | { kind: 'ask'; prompt: string }
  | { kind: 'comment'; input: CreateCommentInput }
  | { kind: 'refuse'; error: string };

export function sectionHolding(session: SessionResponse, path: string, pr: number | undefined): Section | undefined {
  const holding = session.sections.filter((section) => section.files.some((file) => file.path === path));
  return holding.find((section) => section.prNumber === pr) ?? holding.at(-1);
}

export function embedAction(session: SessionResponse, say: EmbedSay): EmbedAction {
  const body = say.text.trim();
  if (!body) return { kind: 'refuse', error: 'there are no words to add' };
  if (say.path === undefined) return { kind: 'ask', prompt: body };
  const section = sectionHolding(session, say.path, say.pr);
  if (!section) return { kind: 'refuse', error: `${say.path} is not among the files this review shows` };
  const subject = { sectionId: section.sectionId, filePath: say.path, side: 'additions' as const, body };
  if (say.line === undefined) {
    return { kind: 'comment', input: { ...subject, range: { start: 0, end: 0 }, lineContent: '', subject: 'file' } };
  }
  const line = say.line;
  const file = parseFiles(section.patchText).find((each) => each.name === say.path);
  if (!file || !rangeInOneHunk(file, { start: line, end: line, side: 'additions', endSide: 'additions' })) {
    return { kind: 'refuse', error: `line ${line} of ${say.path} is outside the diff's hunks, and GitHub takes a line comment only inside one` };
  }
  return { kind: 'comment', input: { ...subject, range: { start: line, end: line }, lineContent: lineContentAt(file, 'additions', line) } };
}

export function EmbedChannel({ session, nav }: { session: SessionResponse; nav: RefObject<DiffViewHandle | null> }) {
  const { slug } = useReview();
  const { mutateAsync: comment } = useCreateComment(slug);
  const { mutateAsync: ask } = useCreateAiRequest(slug);
  const latest = useRef(session);
  latest.current = session;
  const files = [...new Set(session.sections.flatMap((section) => section.files.map((file) => file.path)))].join('\n');

  useEffect(() => {
    window.parent.postMessage({ type: 'cc-review:ready', files: files ? files.split('\n') : [] }, '*');
  }, [files]);

  useEffect(() => {
    function show(path: string, pr: number | undefined) {
      const section = sectionHolding(latest.current, path, pr);
      if (section) nav.current?.scrollToFile({ sectionKey: section.sectionKey, path });
    }
    function heard(event: MessageEvent<EmbedMessage>) {
      if (event.source !== window.parent) return;
      const message = event.data;
      if (message.type === 'cc-review:show') return show(message.path, message.pr);
      if (message.type !== 'cc-review:say') return;
      const said = (error?: string) => window.parent.postMessage({ type: 'cc-review:said', id: message.id, ...(error ? { error } : {}) }, '*');
      const action = embedAction(latest.current, message);
      if (action.kind === 'refuse') return said(action.error);
      if (message.path !== undefined) show(message.path, message.pr);
      (action.kind === 'ask' ? ask(action.prompt) : comment(action.input)).then(() => said(), (error: Error) => said(error.message));
    }
    window.addEventListener('message', heard);
    return () => window.removeEventListener('message', heard);
  }, [nav, ask, comment]);

  return null;
}
