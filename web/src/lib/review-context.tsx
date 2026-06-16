import type { ReactNode } from 'react';
import { createSubjectContext } from '@cc-interact/react';

// The review-on-screen identity: slug as the subject, displayed version as the
// scope. Backed by the library's generic subject context; the review-specific
// slug/version field names live on top so existing consumers are untouched.
const { SubjectProvider, useSubject } = createSubjectContext<string, number | undefined>();

export interface ReviewContextValue {
  slug: string;
  version?: number;
}

export function ReviewProvider({
  value,
  children,
}: {
  value: ReviewContextValue;
  children: ReactNode;
}) {
  return (
    <SubjectProvider value={{ subject: value.slug, scope: value.version }}>
      {children}
    </SubjectProvider>
  );
}

export function useReview(): ReviewContextValue {
  const { subject, scope } = useSubject();
  return scope === undefined ? { slug: subject } : { slug: subject, version: scope };
}
