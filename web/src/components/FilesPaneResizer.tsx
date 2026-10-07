import { useRef, useState } from 'react';
import type { KeyboardEvent, PointerEvent } from 'react';
import { FILES_DEFAULT_WIDTH_PX, FILES_MAX_WIDTH_PX, FILES_MIN_WIDTH_PX, useLayout } from '../lib/layout';

const KEY_STEP_PX = 16;

export function FilesPaneResizer() {
  const { filesWidth: width, setFilesWidth: setWidth } = useLayout();
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
      className="pane-resizer"
      role="separator"
      aria-orientation="vertical"
      aria-label="Resize file tree"
      aria-valuemin={FILES_MIN_WIDTH_PX}
      aria-valuemax={FILES_MAX_WIDTH_PX}
      aria-valuenow={width}
      tabIndex={0}
      data-dragging={dragging || undefined}
      onPointerDown={onPointerDown}
      onPointerMove={onPointerMove}
      onPointerUp={onPointerUp}
      onPointerCancel={onPointerUp}
      onDoubleClick={() => setWidth(FILES_DEFAULT_WIDTH_PX)}
      onKeyDown={onKeyDown}
    />
  );
}
