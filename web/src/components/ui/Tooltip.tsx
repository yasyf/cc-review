import { cloneElement, useEffect, useId, useLayoutEffect, useRef, useState } from 'react';
import type { ReactElement, ReactNode } from 'react';
import { createPortal } from 'react-dom';
import { placeFloating } from './floating';
import type { FloatingPosition, Placement } from './floating';

const OPEN_DELAY_MS = 300;

export function Tooltip({
  label,
  shortcut,
  placement = 'top',
  describe = true,
  children,
}: {
  label: ReactNode;
  shortcut?: string;
  placement?: Placement;
  describe?: boolean;
  children: ReactElement<{ 'aria-describedby'?: string }>;
}) {
  const id = useId();
  const anchorRef = useRef<HTMLSpanElement>(null);
  const tipRef = useRef<HTMLDivElement>(null);
  const timer = useRef<number | undefined>(undefined);
  const [open, setOpen] = useState(false);
  const [position, setPosition] = useState<FloatingPosition | null>(null);

  useEffect(() => () => window.clearTimeout(timer.current), []);

  useLayoutEffect(() => {
    const anchor = anchorRef.current?.firstElementChild;
    const tip = tipRef.current;
    if (!open || !anchor || !tip) return;
    setPosition(placeFloating(anchor.getBoundingClientRect(), tip.getBoundingClientRect(), placement, 'center'));
  }, [open, placement]);

  function show() {
    window.clearTimeout(timer.current);
    timer.current = window.setTimeout(() => setOpen(true), OPEN_DELAY_MS);
  }

  function hide() {
    window.clearTimeout(timer.current);
    setOpen(false);
    setPosition(null);
  }

  return (
    <span
      ref={anchorRef}
      className="tooltip-anchor"
      onMouseEnter={show}
      onMouseLeave={hide}
      onFocus={show}
      onBlur={hide}
      onMouseDown={hide}
      onKeyDown={(e) => {
        if (e.key === 'Escape' && open) hide();
      }}
    >
      {describe ? cloneElement(children, { 'aria-describedby': id }) : children}
      {open
        ? createPortal(
            <div
              ref={tipRef}
              id={id}
              role="tooltip"
              className="tooltip"
              style={position ?? { left: 0, top: 0, visibility: 'hidden' }}
            >
              <span className="tooltip-label">{label}</span>
              {shortcut ? <kbd>{shortcut}</kbd> : null}
            </div>,
            document.body,
          )
        : null}
    </span>
  );
}
