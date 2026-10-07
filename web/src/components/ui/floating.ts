const GAP_PX = 6;
const VIEWPORT_MARGIN_PX = 8;

export type Placement = 'top' | 'bottom';

export interface FloatingPosition {
  left: number;
  top: number;
}

export interface Rect {
  left: number;
  top: number;
  width: number;
  height: number;
}

export function pointRect(x: number, y: number): Rect {
  return { left: x, top: y, width: 0, height: 0 };
}

export function placeFloating(
  anchor: Rect,
  floating: { width: number; height: number },
  placement: Placement,
  align: 'center' | 'start',
): FloatingPosition {
  const above = anchor.top - floating.height - GAP_PX;
  const below = anchor.top + anchor.height + GAP_PX;
  const fitsAbove = above >= VIEWPORT_MARGIN_PX;
  const fitsBelow = below + floating.height <= window.innerHeight - VIEWPORT_MARGIN_PX;
  const top = placement === 'top' ? (fitsAbove || !fitsBelow ? above : below) : fitsBelow || !fitsAbove ? below : above;
  const preferredLeft = align === 'center' ? anchor.left + anchor.width / 2 - floating.width / 2 : anchor.left;
  const maxLeft = window.innerWidth - floating.width - VIEWPORT_MARGIN_PX;
  return { left: Math.max(VIEWPORT_MARGIN_PX, Math.min(preferredLeft, maxLeft)), top };
}
