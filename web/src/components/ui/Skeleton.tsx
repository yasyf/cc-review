const LINE_WIDTHS = [92, 76, 84, 64, 88, 70];

export function Skeleton({ lines = 1, label, className }: { lines?: number; label: string; className?: string }) {
  return (
    <div className={className ? `skeleton-stack ${className}` : 'skeleton-stack'} role="status" aria-label={label}>
      {Array.from({ length: lines }, (_, i) => (
        <span key={i} className="skeleton" style={{ width: `${LINE_WIDTHS[i % LINE_WIDTHS.length]}%` }} />
      ))}
    </div>
  );
}
