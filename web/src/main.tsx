import { StrictMode } from 'react';
import { createRoot } from 'react-dom/client';
import { QueryClientProvider } from '@tanstack/react-query';
import { RouterProvider } from '@tanstack/react-router';
import { WorkerPoolContextProvider } from '@pierre/diffs/react';
import '@cc-interact/react/base.css';
import { queryClient } from './lib/api';
import { router } from './router';
import { applyThemeMode, readThemeMode } from './lib/theme';
import { highlighterOptions, poolOptions } from './worker';
import './styles/tokens.css';
import './styles/layout.css';
import './styles/controls.css';
import './styles/diff.css';
import './styles/threads.css';
import './styles/pr.css';

applyThemeMode(readThemeMode());

const root = document.getElementById('root');
if (!root) throw new Error('missing #root');

createRoot(root).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <WorkerPoolContextProvider poolOptions={poolOptions} highlighterOptions={highlighterOptions}>
        <RouterProvider router={router} />
      </WorkerPoolContextProvider>
    </QueryClientProvider>
  </StrictMode>,
);
