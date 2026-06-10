import { createContext, useContext } from 'react';

export interface ReviewContextValue {
  slug: string;
  version?: number;
}

const ReviewContext = createContext<ReviewContextValue | null>(null);

export const ReviewProvider = ReviewContext.Provider;

export function useReview(): ReviewContextValue {
  const value = useContext(ReviewContext);
  if (!value) throw new Error('useReview must be used within ReviewProvider');
  return value;
}
