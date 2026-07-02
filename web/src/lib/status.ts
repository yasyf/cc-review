import type { ReviewStatus } from './types';

// The one source of the expired/closed copy, shared by the banner sites and
// the status.changed toast. Submitted copy stays site-specific because it
// references the frozen feedback.
export const STATUS_NOTICES: Record<Exclude<ReviewStatus, 'open' | 'submitted'>, string> = {
  expired: 'Review expired after inactivity — run /cc-review:start to resume.',
  closed: 'Review closed without feedback.',
};
