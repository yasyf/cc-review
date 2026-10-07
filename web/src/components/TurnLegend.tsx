import { TURN_PALETTE_SIZE } from '../lib/attribution';
import type { Turn } from '../lib/types';
import { useViewPrefs } from '../lib/view-prefs';
import { Tooltip } from './ui/Tooltip';

export function TurnLegend({ turns }: { turns: readonly Turn[] }) {
  const { activeTurnId, setActiveTurnId } = useViewPrefs();

  if (turns.length === 0) return null;

  return (
    <div className="turn-legend">
      {turns.map((turn, i) => {
        const seq = i + 1;
        return (
          <Tooltip key={turn.id} label={turn.prompt} placement="bottom">
            <button
              type="button"
              className="turn-legend-chip"
              aria-pressed={activeTurnId === turn.id}
              onClick={() => setActiveTurnId(activeTurnId === turn.id ? null : turn.id)}
            >
              <span
                className="turn-dot"
                style={{ background: `var(--turn-${seq % TURN_PALETTE_SIZE})` }}
              />
              T{seq}
            </button>
          </Tooltip>
        );
      })}
    </div>
  );
}
