import type { MainTab } from '../lib/layout';

export interface TabSpec {
  id: MainTab;
  label: string;
  count?: number;
}

export function ReviewTabs({ tabs, active, onSelect }: { tabs: readonly TabSpec[]; active: MainTab; onSelect(tab: MainTab): void }) {
  return (
    <div className="tabs" role="tablist" aria-label="Review">
      {tabs.map((tab) => (
        <button
          key={tab.id}
          type="button"
          role="tab"
          className="tab"
          aria-selected={active === tab.id}
          onClick={() => onSelect(tab.id)}
        >
          {tab.label}
          {tab.count !== undefined ? <span className="count">{tab.count}</span> : null}
        </button>
      ))}
    </div>
  );
}
