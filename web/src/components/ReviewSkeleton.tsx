import { Skeleton } from './ui/Skeleton';

const FILE_BLOCKS = [6, 4, 8];

export function ReviewSkeleton() {
  return (
    <div className="app review-skeleton" aria-busy="true">
      <header className="submit-bar">
        <Skeleton lines={1} label="Loading review" className="skeleton-header" />
      </header>
      <div className="body">
        <div className="sidebar">
          <Skeleton lines={10} label="Loading files" className="skeleton-sidebar" />
        </div>
        <main className="main">
          <Skeleton lines={1} label="Loading toolbar" className="skeleton-toolbar" />
          <div className="diff skeleton-diff">
            {FILE_BLOCKS.map((lines, i) => (
              <section key={i} className="skeleton-file">
                <span className="skeleton skeleton-file-head" />
                <Skeleton lines={lines} label="Loading diff" />
              </section>
            ))}
          </div>
        </main>
      </div>
    </div>
  );
}
