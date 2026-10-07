// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { Dialog } from './Dialog';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;
let opener: HTMLButtonElement;

beforeEach(() => {
  opener = document.createElement('button');
  document.body.appendChild(opener);
  opener.focus();
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  document.body.innerHTML = '';
});

function renderDialog(open: boolean, onClose = vi.fn()) {
  act(() =>
    root.render(
      <Dialog open={open} onClose={onClose} title="Settings" footer={<button type="button">Save</button>}>
        <input aria-label="name" />
      </Dialog>,
    ),
  );
  return onClose;
}

function keydown(target: Element, key: string, shiftKey = false) {
  act(() => {
    target.dispatchEvent(new KeyboardEvent('keydown', { key, shiftKey, bubbles: true }));
  });
}

describe('Dialog', () => {
  it('renders nothing while closed', () => {
    renderDialog(false);
    expect(document.querySelector('[role="dialog"]')).toBeNull();
  });

  it('is a labelled modal that takes focus', () => {
    renderDialog(true);
    const dialog = document.querySelector('[role="dialog"]')!;
    expect(dialog.getAttribute('aria-modal')).toBe('true');
    expect(document.getElementById(dialog.getAttribute('aria-labelledby')!)?.textContent).toBe('Settings');
    expect(document.activeElement).toBe(dialog);
  });

  it('closes on Escape', () => {
    const onClose = renderDialog(true);
    keydown(document.querySelector('[role="dialog"]')!, 'Escape');
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('closes on a scrim click but not on a click inside', () => {
    const onClose = renderDialog(true);
    act(() => {
      document.querySelector('[role="dialog"]')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();
    act(() => {
      document.querySelector('.dialog-scrim')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(onClose).toHaveBeenCalledTimes(1);
  });

  it('traps Tab between the first and last focusable elements', () => {
    renderDialog(true);
    const focusable = [...document.querySelectorAll<HTMLElement>('[role="dialog"] button, [role="dialog"] input')];
    const first = focusable[0];
    const last = focusable[focusable.length - 1];
    expect(last.textContent).toBe('Save');

    last.focus();
    keydown(last, 'Tab');
    expect(document.activeElement).toBe(first);

    keydown(first, 'Tab', true);
    expect(document.activeElement).toBe(last);
  });

  it('returns focus to the opener when it closes', () => {
    renderDialog(true);
    renderDialog(false);
    expect(document.activeElement).toBe(opener);
  });
});
