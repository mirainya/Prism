import React, { RefObject, useEffect, useRef, useState } from 'react';
import { createPortal } from 'react-dom';

export type DialogMotion = 'center' | 'left' | 'right' | 'bottom' | 'fade';

interface DialogProps {
  open: boolean;
  onClose: () => void;
  children: React.ReactNode;
  role?: 'dialog' | 'alertdialog';
  ariaLabel?: string;
  ariaLabelledby?: string;
  ariaDescribedby?: string;
  containerClassName?: string;
  panelClassName?: string;
  motion?: DialogMotion;
  dismissible?: boolean;
  initialFocusRef?: RefObject<HTMLElement | null>;
}

const transitionMilliseconds = 200;
const dialogStack: symbol[] = [];
let bodyLockCount = 0;
let previousBodyOverflow = '';

const lockBody = () => {
  if (bodyLockCount === 0) {
    previousBodyOverflow = document.body.style.overflow;
    document.body.style.overflow = 'hidden';
  }
  bodyLockCount += 1;
};

const unlockBody = () => {
  bodyLockCount = Math.max(0, bodyLockCount - 1);
  if (bodyLockCount === 0) document.body.style.overflow = previousBodyOverflow;
};

const focusableSelector = [
  'button:not([disabled])',
  '[href]',
  'input:not([disabled])',
  'select:not([disabled])',
  'textarea:not([disabled])',
  '[tabindex]:not([tabindex="-1"])',
].join(',');

const motionClasses: Record<DialogMotion, { open: string; closed: string }> = {
  center: {
    open: 'translate-y-0 scale-100 opacity-100',
    closed: 'translate-y-3 scale-[0.98] opacity-0',
  },
  right: {
    open: 'translate-x-0 opacity-100',
    closed: 'translate-x-full opacity-95',
  },
  left: {
    open: 'translate-x-0 opacity-100',
    closed: '-translate-x-full opacity-95',
  },
  bottom: {
    open: 'translate-y-0 opacity-100',
    closed: 'translate-y-full opacity-95',
  },
  fade: {
    open: 'opacity-100',
    closed: 'opacity-0',
  },
};

export const Dialog: React.FC<DialogProps> = ({
  open,
  onClose,
  children,
  role = 'dialog',
  ariaLabel,
  ariaLabelledby,
  ariaDescribedby,
  containerClassName = 'items-center justify-center p-4',
  panelClassName = '',
  motion = 'center',
  dismissible = true,
  initialFocusRef,
}) => {
  const [mounted, setMounted] = useState(open);
  const [visible, setVisible] = useState(false);
  const panelRef = useRef<HTMLDivElement>(null);
  const dialogID = useRef(Symbol('dialog'));
  const onCloseRef = useRef(onClose);
  const dismissibleRef = useRef(dismissible);
  const contentRef = useRef(children);
  onCloseRef.current = onClose;
  dismissibleRef.current = dismissible;
  if (open) contentRef.current = children;

  useEffect(() => {
    let frame = 0;
    let timer = 0;
    if (open) {
      setMounted(true);
      frame = window.requestAnimationFrame(() => setVisible(true));
    } else {
      setVisible(false);
      timer = window.setTimeout(() => setMounted(false), transitionMilliseconds);
    }
    return () => {
      if (frame) window.cancelAnimationFrame(frame);
      if (timer) window.clearTimeout(timer);
    };
  }, [open]);

  useEffect(() => {
    if (!mounted) return;
    const id = dialogID.current;
    const previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    dialogStack.push(id);
    lockBody();

    const onKeyDown = (event: KeyboardEvent) => {
      if (dialogStack[dialogStack.length - 1] !== id) return;
      if (event.key === 'Escape' && dismissibleRef.current) {
        event.preventDefault();
        onCloseRef.current();
        return;
      }
      if (event.key !== 'Tab' || !panelRef.current) return;
      const focusable = Array.from(panelRef.current.querySelectorAll<HTMLElement>(focusableSelector));
      if (focusable.length === 0) {
        event.preventDefault();
        panelRef.current.focus();
        return;
      }
      const first = focusable[0];
      const last = focusable[focusable.length - 1];
      if (event.shiftKey && document.activeElement === first) {
        event.preventDefault();
        last.focus();
      } else if (!event.shiftKey && document.activeElement === last) {
        event.preventDefault();
        first.focus();
      }
    };

    document.addEventListener('keydown', onKeyDown);
    return () => {
      document.removeEventListener('keydown', onKeyDown);
      const index = dialogStack.lastIndexOf(id);
      if (index >= 0) dialogStack.splice(index, 1);
      unlockBody();
      previousFocus?.focus();
    };
  }, [mounted]);

  useEffect(() => {
    if (!open || !mounted) return;
    const timer = window.setTimeout(() => {
      const target = initialFocusRef?.current
        || panelRef.current?.querySelector<HTMLElement>(focusableSelector)
        || panelRef.current;
      target?.focus();
    }, 0);
    return () => window.clearTimeout(timer);
  }, [initialFocusRef, mounted, open]);

  if (!mounted) return null;

  const stateClass = visible ? motionClasses[motion].open : motionClasses[motion].closed;
  return createPortal(
    <div
      className={`fixed inset-0 z-50 flex ${containerClassName} ${visible ? 'pointer-events-auto' : 'pointer-events-none'}`}
      data-state={visible ? 'open' : 'closed'}
    >
      <div
        aria-hidden="true"
        className={`absolute inset-0 bg-black/45 backdrop-blur-sm transition-opacity duration-200 ease-out motion-reduce:transition-none ${visible ? 'opacity-100' : 'opacity-0'}`}
        onClick={dismissible ? onClose : undefined}
      />
      <div
        ref={panelRef}
        tabIndex={-1}
        role={role}
        aria-modal="true"
        aria-label={ariaLabel}
        aria-labelledby={ariaLabelledby}
        aria-describedby={ariaDescribedby}
        className={`relative transform-gpu transition-[transform,opacity] duration-200 ease-out motion-reduce:transition-none ${stateClass} ${panelClassName}`}
      >
        {contentRef.current}
      </div>
    </div>,
    document.body,
  );
};
