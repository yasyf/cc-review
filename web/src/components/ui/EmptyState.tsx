import type { ReactNode } from 'react';
import { Icon } from './Icon';
import type { IconName } from './icons';

export function EmptyState({
  icon,
  title,
  tone = 'neutral',
  action,
  children,
}: {
  icon: IconName;
  title: string;
  tone?: 'neutral' | 'danger';
  action?: ReactNode;
  children?: ReactNode;
}) {
  return (
    <div className={`empty-state empty-state-${tone}`} role={tone === 'danger' ? 'alert' : 'status'}>
      <Icon name={icon} size={24} className="empty-state-icon" />
      <div className="empty-state-title">{title}</div>
      {children ? <div className="empty-state-body">{children}</div> : null}
      {action ? <div className="empty-state-action">{action}</div> : null}
    </div>
  );
}
