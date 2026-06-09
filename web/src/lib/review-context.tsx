import { createContext, useContext } from 'react';
import type { VersionKey } from './api';

export interface ReviewContextValue {
  reviewId: string;
  token: string;
  version: VersionKey;
}

const ReviewContext = createContext<ReviewContextValue | null>(null);

export const ReviewProvider = ReviewContext.Provider;

export function useReview(): ReviewContextValue {
  const value = useContext(ReviewContext);
  if (!value) throw new Error('useReview must be used within ReviewProvider');
  return value;
}
