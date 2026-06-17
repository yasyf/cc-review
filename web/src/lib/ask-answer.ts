import { useState } from 'react';
import { clearAskDraft, readAskDraft, writeAskDraft } from './drafts';
import type { Ask, AskAnswer } from './types';

// Selection + draft-persistence for answering an Ask. Shared by the comment-thread
// QuestionCard and the AI-bar result card so the toggle rules (single-select clears
// Other, multiSelect accumulates) and the cross-remount draft live in one place.
// `draftKey` namespaces the stored draft (a reply id, or `ai:<requestId>`).
export interface AskAnswerState {
  selected: string[];
  otherChosen: boolean;
  otherText: string;
  notes: string;
  focusedLabel: string | null;
  setFocusedLabel(label: string | null): void;
  toggleOption(label: string): void;
  toggleOther(): void;
  updateOtherText(text: string): void;
  updateNotes(text: string): void;
  canSubmit: boolean;
  answer: AskAnswer;
  clear(): void;
}

export function useAskAnswer(ask: Ask, draftKey: string): AskAnswerState {
  const draft = readAskDraft(draftKey);
  const [selected, setSelected] = useState<string[]>(() => draft?.selected ?? []);
  const [otherChosen, setOtherChosen] = useState(() => draft?.otherChosen ?? false);
  const [otherText, setOtherText] = useState(() => draft?.otherText ?? '');
  const [notes, setNotes] = useState(() => draft?.notes ?? '');
  const [focusedLabel, setFocusedLabel] = useState<string | null>(null);
  const multiSelect = ask.multiSelect === true;

  function persist(next: Partial<Parameters<typeof writeAskDraft>[1]>) {
    writeAskDraft(draftKey, { selected, otherChosen, otherText, notes, ...next });
  }

  function toggleOption(label: string) {
    const next = multiSelect
      ? selected.includes(label)
        ? selected.filter((l) => l !== label)
        : [...selected, label]
      : selected.includes(label)
        ? []
        : [label];
    setSelected(next);
    if (!multiSelect) {
      setOtherChosen(false);
      persist({ selected: next, otherChosen: false });
    } else {
      persist({ selected: next });
    }
  }

  function toggleOther() {
    const next = !otherChosen;
    setOtherChosen(next);
    if (!multiSelect) {
      setSelected([]);
      persist({ otherChosen: next, selected: [] });
    } else {
      persist({ otherChosen: next });
    }
  }

  function updateOtherText(text: string) {
    setOtherText(text);
    persist({ otherText: text });
  }

  function updateNotes(text: string) {
    setNotes(text);
    persist({ notes: text });
  }

  const trimmedOther = otherText.trim();
  const trimmedNotes = notes.trim();
  const canSubmit = selected.length > 0 || (otherChosen && trimmedOther !== '');
  const answer: AskAnswer = {
    selected,
    ...(otherChosen && trimmedOther !== '' ? { other: trimmedOther } : {}),
    ...(trimmedNotes !== '' ? { notes: trimmedNotes } : {}),
  };

  return {
    selected,
    otherChosen,
    otherText,
    notes,
    focusedLabel,
    setFocusedLabel,
    toggleOption,
    toggleOther,
    updateOtherText,
    updateNotes,
    canSubmit,
    answer,
    clear: () => clearAskDraft(draftKey),
  };
}
