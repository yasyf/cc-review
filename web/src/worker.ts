import type { WorkerInitializationRenderOptions, WorkerPoolOptions } from '@pierre/diffs/worker';
import type { ThemesType } from '@pierre/diffs';

// Vite bundles the diffs Shiki worker from this static URL. The worker resolves
// shiki@3 from the host dependency graph (matching the main thread), so the
// themed tokens it posts back stay format-compatible across the boundary.
function workerFactory(): Worker {
  return new Worker(new URL('@pierre/diffs/worker/worker.js', import.meta.url), {
    type: 'module',
  });
}

export const themes: ThemesType = { light: 'github-light', dark: 'github-dark' };

export const poolOptions: WorkerPoolOptions = { workerFactory, poolSize: 2 };

export const highlighterOptions: WorkerInitializationRenderOptions = { theme: themes };
