// Presentational, footer-anchored command list. The deck (AiBar) owns the row
// model and keyboard nav (↑↓/⏎/→); this only renders grouped rows and reports
// hover. ⚡ rows run client-side; ✦ rows need Claude and dim when disconnected.

export interface MenuRow {
  id: string;
  group: 'Target' | 'Suggested' | 'Commands' | 'Recent';
  lane: 'instant' | 'agent';
  label: string;
  editText?: string;
  run(): void;
}

const GROUP_ORDER: MenuRow['group'][] = ['Target', 'Suggested', 'Commands', 'Recent'];

export function CommandMenu({
  rows,
  activeIndex,
  connected,
  onHover,
}: {
  rows: MenuRow[];
  activeIndex: number;
  connected: boolean;
  onHover(index: number): void;
}) {
  const indexed = rows.map((row, index) => ({ row, index }));
  const canEdit = rows.some((r) => r.editText !== undefined);

  return (
    <div className="cmdk">
      {rows.length === 0 ? (
        <div className="cmdk-empty">Type to ask Claude, or focus the diff and pick an action.</div>
      ) : (
        <>
          {GROUP_ORDER.map((group) => {
            const groupRows = indexed.filter(({ row }) => row.group === group);
            if (groupRows.length === 0) return null;
            return (
              <div key={group} className="cmdk-group">
                <div className="cmdk-group-label">{group === 'Target' ? 'Matches in this diff' : group}</div>
                {groupRows.map(({ row, index }) => {
                  const disabled = row.lane === 'agent' && !connected;
                  return (
                    <button
                      key={row.id}
                      type="button"
                      className={`cmdk-row${index === activeIndex ? ' cmdk-row-active' : ''}${disabled ? ' cmdk-row-disabled' : ''}`}
                      disabled={disabled}
                      onMouseEnter={() => onHover(index)}
                      onClick={() => {
                        if (!disabled) row.run();
                      }}
                    >
                      <span className="cmdk-lane">{row.lane === 'instant' ? '⚡' : '✦'}</span>
                      <span className="cmdk-label">{row.label}</span>
                    </button>
                  );
                })}
              </div>
            );
          })}
          <div className="cmdk-footer">
            ⚡ instant · ✦ Claude · ↑↓ move · ⏎ run{canEdit ? ' · → edit' : ''}
          </div>
        </>
      )}
    </div>
  );
}
