import { useRef, useState } from 'react';
import type { KeyboardEvent, PointerEvent } from 'react';
import {
  SIDEBAR_DEFAULT_WIDTH_PX,
  SIDEBAR_MAX_WIDTH_PX,
  SIDEBAR_MIN_WIDTH_PX,
  useSidebarLayout,
} from '../lib/sidebar-layout';

const KEY_STEP_PX = 16;

export function SidebarResizer() {
  const { width, setWidth } = useSidebarLayout();
  const drag = useRef<{ startX: number; startWidth: number } | null>(null);
  const [dragging, setDragging] = useState(false);

  function onPointerDown(e: PointerEvent<HTMLDivElement>) {
    e.currentTarget.setPointerCapture(e.pointerId);
    drag.current = { startX: e.clientX, startWidth: width };
    setDragging(true);
  }

  function onPointerMove(e: PointerEvent<HTMLDivElement>) {
    if (!drag.current) return;
    setWidth(drag.current.startWidth + e.clientX - drag.current.startX);
  }

  function onPointerUp() {
    drag.current = null;
    setDragging(false);
  }

  function onKeyDown(e: KeyboardEvent<HTMLDivElement>) {
    if (e.key === 'ArrowLeft') setWidth(width - KEY_STEP_PX);
    else if (e.key === 'ArrowRight') setWidth(width + KEY_STEP_PX);
    else return;
    e.preventDefault();
  }

  return (
    <div
      className="sidebar-resizer"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize sidebar"
      aria-valuemin={SIDEBAR_MIN_WIDTH_PX}
      aria-valuemax={SIDEBAR_MAX_WIDTH_PX}
      aria-valuenow={width}
      tabIndex={0}
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={() => setWidth(SIDEBAR_DEFAULT_WIDTH_PX)}
      onKeyDown={onKeyDown}
    />
  );
}
