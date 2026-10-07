import { useState } from 'react';
import { useCreateComment } from '../lib/api';
import { useReview } from '../lib/review-context';
import { Button } from './ui/Button';

export function SubjectComposer({
  sectionId,
  filePath,
  placeholder,
  submitLabel,
  onDone,
}: {
  sectionId: string;
  filePath: string;
  placeholder: string;
  submitLabel: string;
  onDone?: () => void;
}) {
  const { slug } = useReview();
  const createComment = useCreateComment(slug);
  const [body, setBody] = useState('');

  function submit() {
    const text = body.trim();
    if (!text) return;
    createComment.mutate({
      sectionId,
      filePath,
      side: 'additions',
      range: { start: 0, end: 0 },
      lineContent: '',
      body: text,
      subject: 'file',
    });
    setBody('');
    onDone?.();
  }

  return (
    <div className="composer">
      <textarea
        value={body}
        placeholder={placeholder}
        onChange={(e) => setBody(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Enter' && (e.metaKey || e.ctrlKey)) {
            e.preventDefault();
            submit();
          }
        }}
      />
      <div className="composer-actions">
        {onDone ? (
          <Button variant="ghost" onClick={onDone}>
            Cancel
          </Button>
        ) : null}
        <Button variant="primary" disabled={!body.trim()} onClick={submit}>
          {submitLabel}
        </Button>
      </div>
    </div>
  );
}
