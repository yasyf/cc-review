import { useState } from 'react';
import type { useSubmit } from '../lib/api';
import type { SessionResponse } from '../lib/types';
import { verdictOptions } from '../lib/verdict';
import type { Verdict } from '../lib/verdict';
import { Button } from './ui/Button';
import { Dialog } from './ui/Dialog';

export function SubmitDialog({
  session,
  submit,
  open,
  onClose,
}: {
  session: SessionResponse;
  submit: ReturnType<typeof useSubmit>;
  open: boolean;
  onClose(): void;
}) {
  const [verdict, setVerdict] = useState<Verdict>('COMMENT');
  const [summary, setSummary] = useState('');
  const pr = session.review.kind === 'pr';
  const options = verdictOptions(session.sections, session.pullRequests);

  function send() {
    const body = summary.trim();
    submit.mutate(pr ? { versionNumber: session.version, verdict, summary: body } : { summary: body }, {
      onSuccess: onClose,
    });
  }

  return (
    <Dialog
      open={open}
      onClose={onClose}
      title={pr ? 'Submit review' : 'Send to Claude'}
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            Cancel
          </Button>
          <Button variant="primary" disabled={submit.isPending} onClick={send}>
            {submit.isPending ? 'Submitting…' : pr ? 'Submit review' : 'Send to Claude'}
          </Button>
        </>
      }
    >
      <div className="submit-dialog">
        <textarea
          className="submit-summary"
          autoFocus
          value={summary}
          placeholder={pr ? 'Leave a summary (Markdown)…' : 'Optional note for Claude…'}
          onChange={(e) => setSummary(e.target.value)}
        />
        {pr ? (
          <fieldset className="verdict-options">
            <legend>Verdict</legend>
            {options.map((option) => (
              <label
                key={option.value}
                className={`verdict-option${option.disabledReason ? ' verdict-option-disabled' : ''}`}
              >
                <input
                  type="radio"
                  name="verdict"
                  value={option.value}
                  checked={verdict === option.value}
                  disabled={option.disabledReason !== null}
                  onChange={() => setVerdict(option.value)}
                />
                <span>{option.label}</span>
                {option.disabledReason ? (
                  <span className="verdict-reason">{option.disabledReason}</span>
                ) : null}
              </label>
            ))}
          </fieldset>
        ) : null}
        {submit.error ? <div className="state-error">{submit.error.message}</div> : null}
      </div>
    </Dialog>
  );
}
