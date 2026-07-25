import { createRootRoute, createRoute, createRouter, Outlet } from '@tanstack/react-router';
import { ReviewView } from './routes/review';

const rootRoute = createRootRoute({ component: () => <Outlet /> });

const indexRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/',
  component: () => (
    <div className="state">Open a review link: <code>/s/&lt;hash&gt;</code></div>
  ),
});

export interface ReviewSearch {
  version?: number;
}

const reviewRoute = createRoute({
  getParentRoute: () => rootRoute,
  path: '/s/$slug',
  validateSearch: (search: Record<string, unknown>): ReviewSearch => {
    const raw = search.version;
    const version =
      typeof raw === 'number' ? raw : typeof raw === 'string' && raw !== '' ? Number(raw) : undefined;
    return version === undefined || Number.isNaN(version) ? {} : { version };
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
