import type { ComponentPropsWithRef } from 'react';
import { Icon } from './Icon';
import type { IconName } from './icons';
import { Tooltip } from './Tooltip';

export interface ButtonProps extends ComponentPropsWithRef<'button'> {
  variant?: 'primary' | 'secondary' | 'ghost' | 'danger';
  size?: 'sm' | 'md';
}

export function Button({ variant = 'secondary', size = 'md', className, type = 'button', ...props }: ButtonProps) {
  const classes = ['btn', `btn-${variant}`, size === 'sm' ? 'btn-sm' : null, className].filter(Boolean).join(' ');
  return <button {...props} type={type} className={classes} />;
}

export interface IconButtonProps extends Omit<ButtonProps, 'children' | 'aria-label'> {
  icon: IconName;
  label: string;
  shortcut?: string;
}

export function IconButton({ icon, label, shortcut, variant = 'ghost', size = 'sm', className, ...props }: IconButtonProps) {
  return (
    <Tooltip label={label} {...(shortcut ? { shortcut } : {})} describe={false}>
      <Button
        {...props}
        variant={variant}
        size={size}
        aria-label={label}
        className={className ? `btn-icon ${className}` : 'btn-icon'}
      >
        <Icon name={icon} />
      </Button>
    </Tooltip>
  );
}
