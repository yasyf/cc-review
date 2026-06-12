import { useState } from 'react';
import { useTurnProvenance } from '../lib/api';
import { TURN_PALETTE_SIZE } from '../lib/attribution';
import type { SessionResponse, Turn, TurnDecision } from '../lib/types';

function excerpt(text: string, max = 80): string {
  const flat = text.replace(/\s+/g, ' ').trim();
  return flat.length > max ? `${flat.slice(0, max)}…` : flat;
}

function DecisionRow({ decision }: { decision: TurnDecision }) {
  return (
    <div className="activity-row">
      <span className={`action-chip action-${decision.action}`}>{decision.action}</span>
      {decision.toolName ? <span className="activity-tool">{decision.toolName}</span> : null}
      <span className="activity-detail" title={decision.message}>
        {decision.message || `${decision.source} · ${decision.kind}`}
      </span>
    </div>
  );
}

function TurnProvenance({ turnId, open }: { turnId: string; open: boolean }) {
  const { data, isPending } = useTurnProvenance(turnId, open);

  if (!open) return null;
  if (isPending) return <div className="activity-note">loading tool calls…</div>;
  if (!data || data.provenance_unavailable) {
    return <div className="activity-note">transcript unavailable — no tool-call provenance</div>;
  }
  if (data.provenance.length === 0) {
    return <div className="activity-note">no tool calls in this turn</div>;
  }
  return (
    <>
      {data.provenance.map((item) => (
        <div key={item.event_uuid} className="activity-row">
          <span className="activity-tool">{item.tool_name}</span>
          <span className="activity-detail" title={item.summary}>
            {excerpt(item.summary)}
          </span>
        </div>
      ))}
    </>
  );
}

function TurnActivity({
  turn,
  seq,
  decisions,
}: {
  turn: Turn;
  seq: number;
  decisions: TurnDecision[];
}) {
  const [open, setOpen] = useState(false);

  return (
    <details
      className="turn-activity"
      onToggle={(e) => setOpen((e.currentTarget as HTMLDetailsElement).open)}
    >
      <summary className="turn-activity-summary">
        <span
          className="turn-dot"
          style={{ background: `var(--turn-${seq % TURN_PALETTE_SIZE})` }}
        />
        <span className="turn-activity-label">T{seq}</span>
        <span className="turn-activity-prompt" title={turn.prompt}>
          {excerpt(turn.prompt)}
        </span>
        {decisions.length > 0 ? (
          <span className="activity-count">{decisions.length}</span>
        ) : null}
      </summary>
      <div className="turn-activity-body">
        {decisions.map((decision) => (
          <DecisionRow key={`${decision.tsMs}-${decision.source}-${decision.kind}`} decision={decision} />
        ))}
        <TurnProvenance turnId={turn.id} open={open} />
      </div>
    </details>
  );
}

export function TurnActivityPanel({ session }: { session: SessionResponse }) {
  if (session.turns.length === 0) {
    return <div className="sidebar-empty">No turns recorded for this review yet.</div>;
  }
  return (
    <div className="turn-activity-panel">
      {session.turns.map((turn, i) => (
        <TurnActivity
          key={turn.id}
          turn={turn}
          seq={i + 1}
          decisions={session.turnActivity[turn.id] ?? []}
        />
      ))}
    </div>
  );
}
