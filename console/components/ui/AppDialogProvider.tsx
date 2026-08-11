import React, { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState } from 'react';
import { ConfirmDialog, ConfirmDialogTone } from './ConfirmDialog';

interface DialogOptions {
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  tone?: ConfirmDialogTone;
}

interface DialogRequest extends DialogOptions {
  kind: 'alert' | 'confirm';
  resolve: (confirmed: boolean) => void;
}

interface AppDialogContextValue {
  askConfirmation: (options: DialogOptions) => Promise<boolean>;
  showAlert: (options: DialogOptions) => Promise<void>;
}

const AppDialogContext = createContext<AppDialogContextValue | null>(null);

export const AppDialogProvider: React.FC<{ children: React.ReactNode }> = ({ children }) => {
  const [active, setActive] = useState<DialogRequest | null>(null);
  const activeRef = useRef<DialogRequest | null>(null);
  const queueRef = useRef<DialogRequest[]>([]);

  const enqueue = useCallback((request: DialogRequest) => {
    if (activeRef.current) {
      queueRef.current.push(request);
      return;
    }
    activeRef.current = request;
    setActive(request);
  }, []);

  const settle = useCallback((confirmed: boolean) => {
    const current = activeRef.current;
    if (!current) return;
    current.resolve(confirmed);
    const next = queueRef.current.shift() || null;
    activeRef.current = next;
    setActive(next);
  }, []);

  const askConfirmation = useCallback((options: DialogOptions) => new Promise<boolean>(resolve => {
    enqueue({ ...options, kind: 'confirm', resolve });
  }), [enqueue]);

  const showAlert = useCallback((options: DialogOptions) => new Promise<void>(resolve => {
    enqueue({ ...options, kind: 'alert', resolve: () => resolve() });
  }), [enqueue]);

  useEffect(() => () => {
    activeRef.current?.resolve(false);
    queueRef.current.forEach(request => request.resolve(false));
  }, []);

  const value = useMemo(() => ({ askConfirmation, showAlert }), [askConfirmation, showAlert]);

  return (
    <AppDialogContext.Provider value={value}>
      {children}
      <ConfirmDialog
        open={Boolean(active)}
        title={active?.title || ''}
        description={active?.description || ''}
        confirmLabel={active?.confirmLabel || (active?.kind === 'alert' ? '知道了' : '确认')}
        cancelLabel={active?.cancelLabel || '取消'}
        tone={active?.tone || 'info'}
        showCancel={active?.kind === 'confirm'}
        onConfirm={() => settle(true)}
        onCancel={() => settle(false)}
      />
    </AppDialogContext.Provider>
  );
};

export const useAppDialog = () => {
  const context = useContext(AppDialogContext);
  if (!context) throw new Error('useAppDialog must be used within AppDialogProvider');
  return context;
};
