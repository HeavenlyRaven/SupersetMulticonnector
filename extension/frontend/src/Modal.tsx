import React from 'react';

import { getTheme } from './styles';

interface Props {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  footer?: React.ReactNode;
}

// @apache-superset/core/components has no Modal (only Alert exists in the
// live package) — this is a minimal hand-rolled one, not an assumed SDK
// component. Colors come from getTheme() rather than being hardcoded —
// see styles.ts's doc comment for the white-panel/white-text bug this
// fixes.
export default function Modal({ title, onClose, children, footer }: Props) {
  const t = getTheme();
  return (
    <div
      style={{
        position: 'fixed',
        top: 0,
        left: 0,
        right: 0,
        bottom: 0,
        background: t.overlayBg,
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'center',
        zIndex: 1000,
      }}
      onClick={(e) => {
        if (e.target === e.currentTarget) onClose();
      }}
    >
      <div
        style={{
          background: t.panelBg,
          borderRadius: 6,
          minWidth: 420,
          maxWidth: 560,
          width: '90%',
          boxShadow: '0 4px 24px rgba(0, 0, 0, 0.4)',
        }}
      >
        <div
          style={{
            display: 'flex',
            justifyContent: 'space-between',
            alignItems: 'center',
            padding: '16px 20px',
            borderBottom: `1px solid ${t.border}`,
          }}
        >
          <strong style={{ fontSize: 16, color: t.text }}>{title}</strong>
          <button
            onClick={onClose}
            aria-label="Close"
            style={{ border: 'none', background: 'none', fontSize: 18, cursor: 'pointer', lineHeight: 1, color: t.text }}
          >
            &times;
          </button>
        </div>
        <div style={{ padding: 20, color: t.text }}>{children}</div>
        {footer && (
          <div
            style={{
              padding: '12px 20px',
              borderTop: `1px solid ${t.border}`,
              display: 'flex',
              gap: 8,
              justifyContent: 'flex-end',
            }}
          >
            {footer}
          </div>
        )}
      </div>
    </div>
  );
}
