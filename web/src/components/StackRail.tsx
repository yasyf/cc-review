import { stackCards } from '../lib/stack';
import type { CiState, ReviewState, StackCard } from '../lib/stack';
import { useUnread } from '../lib/unread';
import type { SessionResponse } from '../lib/types';

const CI_LABEL: Record<CiState, string> = {
  success: 'Checks passing',
  failure: 'Checks failing',
  pending: 'Checks running',
  none: 'No checks',
};

const REVIEW_LABEL: Record<ReviewState, string> = {
  approved: 'Approved',
  changes_requested: 'Changes requested',
  commented: 'Commented',
  pending: 'Review requested',
  none: '',
};

function Card({
  card,
  active,
  onSelect,
}: {
  card: StackCard;
  active: boolean;
  onSelect(): void;
}) {
  return (
    <button
      type="button"
      className={`stack-card${active ? ' stack-card-active' : ''}`}
      aria-pressed={active}
      onClick={onSelect}
    >
      <span className="stack-card-head">
        <span className={`ci-dot ci-${card.ci}`} role="img" aria-label={CI_LABEL[card.ci]} />
        {card.pr ? <span className="stack-card-number">#{card.pr.number}</span> : null}
        <span className="stack-card-title">{card.title}</span>
      </span>
      <span className="stack-card-meta">
        {card.review !== 'none' ? (
          <span className={`review-state review-${card.review}`}>{REVIEW_LABEL[card.review]}</span>
        ) : null}
        {card.unread > 0 ? <span className="stack-card-unread">{card.unread} new</span> : null}
        {card.open > 0 ? <span className="stack-card-open">{card.open} open</span> : null}
        <span className="stack-card-progress">
          {card.reviewed}/{card.total}
        </span>
      </span>
      <span className="progress-track">
        <span
          className="progress-fill"
          style={{ width: `${card.total > 0 ? (card.reviewed / card.total) * 100 : 0}%` }}
        />
      </span>
    </button>
  );
}

export function StackRail({
  session,
  scope,
  onScope,
}: {
  session: SessionResponse;
  scope: string | undefined;
  onScope(sectionKey: string | undefined): void;
}) {
  const { seen } = useUnread();
  const cards = stackCards(session, seen).reverse();

  return (
    <nav className="stack-rail" aria-label="Stack">
      <button
        type="button"
        className={`stack-card stack-card-all${scope === undefined ? ' stack-card-active' : ''}`}
        aria-pressed={scope === undefined}
        onClick={() => onScope(undefined)}
      >
        <span className="stack-card-title">All</span>
        <span className="stack-card-progress">{cards.length} sections</span>
      </button>
      {cards.map((card) => (
        <Card
          key={card.sectionKey}
          card={card}
          active={scope === card.sectionKey}
          onSelect={() => onScope(card.sectionKey)}
        />
      ))}
    </nav>
  );
}
