import { setThemeMode, useThemeMode } from '../../lib/theme';
import type { ThemeMode } from '../../lib/theme';
import { Icon } from './Icon';
import type { IconName } from './icons';
import { Tooltip } from './Tooltip';

const MODES: { mode: ThemeMode; icon: IconName; label: string }[] = [
  { mode: 'system', icon: 'monitor', label: 'System theme' },
  { mode: 'light', icon: 'sun', label: 'Light theme' },
  { mode: 'dark', icon: 'moon', label: 'Dark theme' },
];

export function ThemeToggle() {
  const current = useThemeMode();
  return (
    <span className="seg theme-toggle" role="group" aria-label="Color theme">
      {MODES.map(({ mode, icon, label }) => (
        <Tooltip key={mode} label={label} describe={false}>
          <button
            type="button"
            className="seg-btn seg-icon"
            aria-pressed={current === mode}
            aria-label={label}
            onClick={() => setThemeMode(mode)}
          >
            <Icon name={icon} size={14} />
          </button>
        </Tooltip>
      ))}
    </span>
  );
}
