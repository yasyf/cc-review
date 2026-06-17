import type { AskOption } from '../lib/types';

// Presentational, fully controlled. Renders the selectable option grid, the
// always-available "Other" row, and — when any option carries a preview — the
// side-by-side preview pane. Selection state and persistence live in the caller
// (see useAskAnswer); this component only renders and emits.
export function AskOptionPicker({
  options,
  selected,
  otherChosen,
  otherText,
  focusedLabel,
  disabled,
  onToggle,
  onToggleOther,
  onOtherText,
  onFocusLabel,
}: {
  options: AskOption[];
  selected: string[];
  otherChosen: boolean;
  otherText: string;
  focusedLabel: string | null;
  disabled: boolean;
  onToggle(label: string): void;
  onToggleOther(): void;
  onOtherText(text: string): void;
  onFocusLabel(label: string | null): void;
}) {
  const hasPreview = options.some((option) => option.preview !== undefined);
  const focusedOption =
    focusedLabel === null ? undefined : options.find((option) => option.label === focusedLabel);
  const preview =
    focusedOption === undefined
      ? options.find((option) => option.preview !== undefined)?.preview
      : focusedOption.preview;

  const rows = (
    <div className="qc-options">
      {options.map((option) => {
        const isSelected = selected.includes(option.label);
        const isFocused = focusedLabel === option.label;
        return (
          <button
            key={option.label}
            type="button"
            className={`qc-option${isSelected ? ' qc-option-selected' : ''}${isFocused ? ' qc-option-focused' : ''}`}
            disabled={disabled}
            aria-pressed={isSelected}
            onClick={() => onToggle(option.label)}
            onMouseEnter={() => onFocusLabel(option.label)}
            onFocus={() => onFocusLabel(option.label)}
          >
            <span className="qc-option-label">{option.label}</span>
            {option.description ? <span className="qc-option-desc">{option.description}</span> : null}
          </button>
        );
      })}
      <button
        type="button"
        className={`qc-option${otherChosen ? ' qc-option-selected' : ''}`}
        disabled={disabled}
        aria-pressed={otherChosen}
        onClick={onToggleOther}
      >
        <span className="qc-option-label">Other</span>
      </button>
      {otherChosen ? (
        <input
          type="text"
          className="qc-other-input"
          value={otherText}
          placeholder="Your answer…"
          disabled={disabled}
          onChange={(e) => onOtherText(e.target.value)}
        />
      ) : null}
    </div>
  );

  if (!hasPreview) return rows;
  return (
    <div className="qc-grid">
      {rows}
      <pre className={`qc-preview${preview === undefined ? ' qc-preview-empty' : ''}`}>
        {preview ?? 'no preview'}
      </pre>
    </div>
  );
}
