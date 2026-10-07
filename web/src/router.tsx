import { createRootRoute, createRoute, createRouter, Outlet } from '@tanstack/react-router';
import { LandingView } from './routes/landing';
import { ReviewView } from './routes/review';
import { parseMainTab } from './lib/layout';
import type { MainTab } from './lib/layout';

const rootRoute = createRootRoute({ component: () => <Outlet /> });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: LandingView,
});

export interface ReviewSearch {
  version?: number;
  tab?: MainTab;
  section?: string;
}

const reviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/s/$slug',
  validateSearch: (search: Record<string, unknown>): ReviewSearch => {
    const raw = search.version;
    const version =
      typeof raw === 'number' ? raw : typeof raw === 'string' && raw !== '' ? Number(raw) : undefined;
    const tab = parseMainTab(search.tab);
    return {
      ...(version === undefined || Number.isNaN(version) ? {} : { version }),
      ...(tab ? { tab } : {}),
      ...(typeof search.section === 'string' ? { section: search.section } : {}),
    };
  },
  component: ReviewView,
});

const routeTree = rootRoute.addChildren([indexRoute, reviewRoute]);

export const router = createRouter({ routeTree });

declare module '@tanstack/react-router' {
  interface Register {
    router: typeof router;
  }
}
