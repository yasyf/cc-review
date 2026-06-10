// In-progress textarea drafts, keyed by surface. Lives outside React because
// these inputs render inside index-keyed annotation portals: an annotation
// shift or virtualizer release remounts the component, and the remounted
// instance rehydrates its text from here. There is at most one composer, so it
// gets a fixed key; reply boxes are keyed by their comment.
const drafts = new Map<string, string>();

export const composerDraftKey = 'composer';
export const replyDraftKey = (commentId: string) => `reply:${commentId}`;

export function readDraft(key: string): string {
  return drafts.get(key) ?? '';
}

export function writeDraft(key: string, text: string): void {
  if (text) drafts.set(key, text);
  else drafts.delete(key);
}

export function clearDraft(key: string): void {
  drafts.delete(key);
}

// An in-progress ask answer is structured, so it gets its own typed map,
// keyed by the ask reply's id.
export interface AskDraft {
  selected: string[];
  otherChosen: boolean;
  otherText: string;
  notes: string;
}

const askDrafts = new Map<string, AskDraft>();

export function readAskDraft(replyId: string): AskDraft | undefined {
  return askDrafts.get(replyId);
}

export function writeAskDraft(replyId: string, draft: AskDraft): void {
  askDrafts.set(replyId, draft);
}

export function clearAskDraft(replyId: string): void {
  askDrafts.delete(replyId);
}
