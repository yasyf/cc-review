import { useEffect } from 'react';
import type { Dispatch, RefObject, SetStateAction } from 'react';
import type { DiffViewHandle } from '../components/DiffView';

function isTypingTarget(target: EventTarget | null): boolean {
  if (!(target instanceof HTMLElement)) return false;
  if (target.isContentEditable) return true;
  return target.tagName === 'INPUT' || target.tagName === 'TEXTAREA' || target.tagName === 'SELECT';
}

export function useKeyboardShortcuts(
  diffRef: RefObject<DiffViewHandle | null>,
  help: { helpOpen: boolean; setHelpOpen: Dispatch<SetStateAction<boolean>> },
) {
  const { setHelpOpen } = help;
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      // Cmd/Ctrl+K belongs to the Command Deck (AiBar); typing surfaces own
      // every other key (Esc included — that's the composer's).
      if (e.metaKey || e.ctrlKey || e.altKey) return;
      if (isTypingTarget(e.target)) return;
      const diff = diffRef.current;
      switch (e.key) {
        case 'j':
          diff?.focusNextFile();
          break;
        case 'k':
          diff?.focusPrevFile();
          break;
        case 'v':
          diff?.toggleViewedCurrent();
          break;
        case 'c':
          diff?.toggleCollapseCurrent();
          break;
        case 'n':
          diff?.focusNextComment();
          break;
        case 'p':
          diff?.focusPrevComment();
          break;
        case '?':
          setHelpOpen((open) => !open);
          break;
        case 'Escape':
          setHelpOpen(false);
          break;
        default:
          return;
      }
      e.preventDefault();
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [diffRef, setHelpOpen]);
}
