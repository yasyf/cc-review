import { commentSectionKey } from '../lib/diff/items';
import { sectionPullRequest } from '../lib/stack';
import { conversationThreads } from '../lib/threads';
import type { SessionResponse } from '../lib/types';
import { CommentThread } from './CommentThread';
import { SubjectComposer } from './SubjectComposer';

export function ConversationPanel({ session }: { session: SessionResponse }) {
  const threads = conversationThreads(session.comments).sort((a, b) =>
    a.createdAt.localeCompare(b.createdAt),
  );
  const open = session.review.status === 'open';
  return (
    <div className="conversation-panel">
      {session.sections
        .slice()
        .reverse()
        .map((section) => {
          const pr = sectionPullRequest(section, session.pullRequests);
          const label = pr ? `#${pr.number}` : section.branch;
          const sectionThreads = threads.filter((c) => commentSectionKey(c) === section.sectionKey);
          return (
            <section key={section.sectionKey} className="conversation-group">
              {session.sections.length > 1 ? (
                <h3 className="conversation-group-title">
                  {label} {pr ? <span className="dim">{pr.title}</span> : null}
                </h3>
              ) : null}
              {sectionThreads.length === 0 ? <div className="sidebar-empty">No conversation yet.</div> : null}
              {sectionThreads.map((comment) => (
                <CommentThread key={comment.id} commentId={comment.id} />
              ))}
              {open ? (
                <SubjectComposer
                  sectionId={section.sectionId}
                  filePath=""
                  placeholder={`Comment on ${label}…`}
                  submitLabel="Comment"
                />
              ) : null}
            </section>
          );
        })}
    </div>
  );
}
