// @vitest-environment jsdom
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { act, createRef } from 'react';
import { createRoot } from 'react-dom/client';
import type { Root } from 'react-dom/client';
import { Popover } from './Popover';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

let container: HTMLDivElement;
let root: Root;

beforeEach(() => {
  container = document.createElement('div');
  document.body.appendChild(container);
  root = createRoot(container);
});

afterEach(() => {
  act(() => root.unmount());
  document.body.innerHTML = '';
});

describe('Popover', () => {
  it('dismisses an interactive popover on Escape and outside clicks, not inside or on its anchor', () => {
    const anchor = createRef<HTMLButtonElement>();
    const onClose = vi.fn();
    act(() =>
      root.render(
        <>
          <button ref={anchor} type="button">
            open
          </button>
          <Popover anchor={anchor} onClose={onClose} label="Menu">
            <button type="button">item</button>
          </Popover>
        </>,
      ),
    );
    const popover = document.querySelector('[role="dialog"][aria-label="Menu"]')!;
    act(() => {
      popover.querySelector('button')!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
      anchor.current!.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    expect(onClose).not.toHaveBeenCalled();

    act(() => {
      document.body.dispatchEvent(new MouseEvent('mousedown', { bubbles: true }));
    });
    act(() => {
      window.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape' }));
    });
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it('renders a non-interactive hover card that ignores the pointer', () => {
    act(() =>
      root.render(
        <Popover anchor={{ left: 10, top: 10, width: 0, height: 0 }} interactive={false}>
          note
        </Popover>,
      ),
    );
    const card = document.querySelector('.popover')!;
    expect(card.getAttribute('role')).toBe('tooltip');
    expect(card.classList.contains('popover-interactive')).toBe(false);
  });
});
