import { useLayoutEffect, useRef, useState } from 'react';
import { automatedThreads, conversationThreads } from '../lib/threads';
import type { PullRequest, Section, SessionResponse } from '../lib/types';
import { CommentThread } from './CommentThread';
import { SubjectComposer } from './SubjectComposer';
import { Button } from './ui/Button';
import { Icon } from './ui/Icon';
import { Markdown } from './ui/Markdown';

function Description({ body }: { body: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const [expanded, setExpanded] = useState(false);
  const [overflows, setOverflows] = useState(false);

  useLayoutEffect(() => {
    const el = ref.current;
    if (el) setOverflows(el.scrollHeight > el.clientHeight + 1);
  }, [body]);

  if (!body.trim()) return <p className="dim overview-empty">No description provided.</p>;
  return (
    <div className="description">
      <div ref={ref} className={`description-body${expanded ? ' description-open' : ''}${overflows && !expanded ? ' description-clipped' : ''}`}>
        <Markdown source={body} />
      </div>
      {overflows || expanded ? (
        <Button size="sm" variant="ghost" onClick={() => setExpanded(!expanded)}>
          <Icon name={expanded ? 'chevron-down' : 'chevron-right'} size={12} />
          {expanded ? 'Show less' : 'Show more'}
        </Button>
      ) : null}
    </div>
  );
}

export function BotsDisclosure({ session }: { session: SessionResponse }) {
  const bots = automatedThreads(session.comments).sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  if (bots.length === 0) return null;
  return (
    <details className="bots">
      <summary>
        <Icon name="chevron-right" size={12} className="bots-caret" />
        <Icon name="bot" size={14} />
        Bots &amp; automation
        <span className="count">{bots.length}</span>
      </summary>
      <div className="bots-body">
        {bots.map((c) => (
          <CommentThread key={c.id} commentId={c.id} context />
        ))}
      </div>
    </details>
  );
}

function ConversationComposer({ sectionId, number }: { sectionId: string; number: number }) {
  const [composing, setComposing] = useState(false);
  if (!composing) {
    return (
      <button type="button" className="reply-trigger" onClick={() => setComposing(true)}>
        Comment on #{number}…
      </button>
    );
  }
  return (
    <SubjectComposer
      sectionId={sectionId}
      filePath=""
      placeholder={`Comment on #${number}…`}
      submitLabel="Comment"
      onDone={() => setComposing(false)}
    />
  );
}

export function OverviewPanel({
  session,
  section,
  pr,
}: {
  session: SessionResponse;
  section: Section;
  pr: PullRequest;
}) {
  const threads = conversationThreads(session.comments).sort((a, b) => a.createdAt.localeCompare(b.createdAt));
  const open = session.review.status === 'open';
  return (
    <div className="overview">
      <Description body={pr.body} />
      <h2 className="overview-heading">Conversation</h2>
      {threads.length === 0 ? <p className="dim overview-empty">No conversation yet.</p> : null}
      <div className="overview-threads">
        {threads.map((c) => (
          <CommentThread key={c.id} commentId={c.id} />
        ))}
      </div>
      {open ? <ConversationComposer sectionId={section.sectionId} number={pr.number} /> : null}
      <BotsDisclosure session={session} />
    </div>
  );
}
