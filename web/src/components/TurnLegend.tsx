import { TURN_PALETTE_SIZE } from '../lib/attribution';
import type { Turn } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';

export function TurnLegend({ turns }: { turns: readonly Turn[] }) {
  const { activeTurnId, setActiveTurnId } = useViewPrefs();

  if (turns.length === 0) return null;

  return (
    <div className="turn-legend">
      {turns.map((turn, i) => {
        const seq = i + 1;
        return (
          <button
            key={turn.id}
            type="button"
            className="turn-legend-chip"
            aria-pressed={activeTurnId === turn.id}
            title={turn.prompt}
            onClick={() => setActiveTurnId(activeTurnId === turn.id ? null : turn.id)}
          >
            <span
              className="turn-dot"
              style={{ background: `var(--turn-${seq % TURN_PALETTE_SIZE})` }}
            />
            T{seq}
          </button>
        );
      })}
    </div>
  );
}
