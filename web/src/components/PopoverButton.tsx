import { useRef, useState } from 'react';
import type { ReactNode } from 'react';
import { Button } from './ui/Button';
import { Popover } from './ui/Popover';

export function PopoverButton({
  label,
  popoverLabel,
  className,
  children,
}: {
  label: ReactNode;
  popoverLabel: string;
  className?: string;
  children: ReactNode;
}) {
  const anchor = useRef<HTMLButtonElement>(null);
  const [open, setOpen] = useState(false);
  return (
    <>
      <Button
        ref={anchor}
        size="sm"
        variant="ghost"
        aria-expanded={open}
        className={className}
        onClick={() => setOpen(!open)}
      >
        {label}
      </Button>
      {open ? (
        <Popover anchor={anchor} label={popoverLabel} onClose={() => setOpen(false)}>
          {children}
        </Popover>
      ) : null}
    </>
  );
}
