import { useEffect, useLayoutEffect, useRef, useState } from 'react';
import type { ReactNode, RefObject } from 'react';
import { createPortal } from 'react-dom';
import { placeFloating } from './floating';
import type { FloatingPosition, Placement, Rect } from './floating';

export type PopoverAnchor = RefObject<HTMLElement | null> | Rect;

function anchorRect(anchor: PopoverAnchor): Rect | null {
  if ('current' in anchor) return anchor.current?.getBoundingClientRect() ?? null;
  return anchor;
}

export function Popover({
  anchor,
  onClose,
  placement = 'bottom',
  interactive = true,
  className,
  label,
  children,
}: {
  anchor: PopoverAnchor;
  onClose?: () => void;
  placement?: Placement;
  interactive?: boolean;
  className?: string;
  label?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);
  const [position, setPosition] = useState<FloatingPosition | null>(null);

  useLayoutEffect(() => {
    const floating = ref.current;
    const target = anchorRect(anchor);
    if (!floating || !target) return;
    setPosition(placeFloating(target, floating.getBoundingClientRect(), placement, 'start'));
  }, [anchor, placement]);

  useEffect(() => {
    if (!interactive || !onClose) return;
    const close = onClose;
    function onKey(e: KeyboardEvent) {
      if (e.key === 'Escape') {
        e.stopPropagation();
        close();
      }
    }
    function onDown(e: MouseEvent) {
      const target = e.target as Node;
      const anchorEl = 'current' in anchor ? anchor.current : null;
      if (ref.current?.contains(target) || anchorEl?.contains(target)) return;
      close();
    }
    window.addEventListener('keydown', onKey, true);
    window.addEventListener('mousedown', onDown);
    return () => {
      window.removeEventListener('keydown', onKey, true);
      window.removeEventListener('mousedown', onDown);
    };
  }, [interactive, onClose, anchor]);

  return createPortal(
    <div
      ref={ref}
      className={['popover', interactive ? 'popover-interactive' : null, className].filter(Boolean).join(' ')}
      {...(interactive ? { role: 'dialog', 'aria-label': label } : { role: 'tooltip' })}
      style={position ?? { left: 0, top: 0, visibility: 'hidden' }}
    >
      {children}
    </div>,
    document.body,
  );
}
