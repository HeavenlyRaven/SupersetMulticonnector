import type { CSSProperties } from 'react';

// @apache-superset/core/components currently exports only Alert (verified
// against the live 0.1.0 package — no Button/Input/Select/Table exist yet,
// this being a pre-1.0 framework — and it exposes no theme tokens either).
// These are small, deliberately plain style helpers standing in until a
// real component library ships.
//
// Every style below sets its own background AND text color explicitly,
// rather than mixing an explicit background with inherited text color.
// Live-verified bug this fixes: Modal's panel had a hardcoded white
// background, but its title/labels/helper text had no color set, so they
// inherited Superset's page-wide text color — which is near-white when
// Superset is in dark mode (as SQL Lab was in the screenshot that
// surfaced this). White text on a white panel is invisible; only the
// browser's native placeholder styling (fixed gray, not inherited) and
// native <select>/<input> theming stayed legible. getTheme() reads the
// live page's own computed background color to pick a matching palette,
// instead of assuming a theme.
export interface Palette {
  overlayBg: string;
  panelBg: string;
  text: string;
  subtleText: string;
  border: string;
  inputBg: string;
  inputText: string;
  headerBorder: string;
  rowBorder: string;
  buttonDefaultBg: string;
  buttonDefaultText: string;
  buttonDefaultBorder: string;
  buttonPrimaryBg: string;
  buttonPrimaryBorder: string;
  buttonPrimaryText: string;
  buttonDangerBg: string;
  buttonDangerBorder: string;
  buttonDangerText: string;
}

const light: Palette = {
  overlayBg: 'rgba(0, 0, 0, 0.45)',
  panelBg: '#fff',
  text: 'rgba(0, 0, 0, 0.88)',
  subtleText: 'rgba(0, 0, 0, 0.45)',
  border: '#d9d9d9',
  inputBg: '#fff',
  inputText: 'rgba(0, 0, 0, 0.88)',
  headerBorder: '#eee',
  rowBorder: '#f0f0f0',
  buttonDefaultBg: '#fff',
  buttonDefaultText: 'rgba(0, 0, 0, 0.88)',
  buttonDefaultBorder: '#d9d9d9',
  buttonPrimaryBg: '#1677ff',
  buttonPrimaryBorder: '#1677ff',
  buttonPrimaryText: '#fff',
  buttonDangerBg: '#fff',
  buttonDangerBorder: '#ff4d4f',
  buttonDangerText: '#ff4d4f',
};

// Values chosen to match antd's own dark-theme tokens (Superset's
// component library), since Superset's dark SQL Lab is antd-based —
// panelBg/inputBg/border/buttonPrimaryBg below are antd v5's dark
// colorBgElevated/colorBgContainer/colorBorder/colorPrimary.
const dark: Palette = {
  overlayBg: 'rgba(0, 0, 0, 0.65)',
  panelBg: '#1f1f1f',
  text: 'rgba(255, 255, 255, 0.85)',
  subtleText: 'rgba(255, 255, 255, 0.45)',
  border: '#424242',
  inputBg: '#141414',
  inputText: 'rgba(255, 255, 255, 0.85)',
  headerBorder: '#303030',
  rowBorder: '#303030',
  buttonDefaultBg: '#1f1f1f',
  buttonDefaultText: 'rgba(255, 255, 255, 0.85)',
  buttonDefaultBorder: '#424242',
  buttonPrimaryBg: '#1668dc',
  buttonPrimaryBorder: '#1668dc',
  buttonPrimaryText: '#fff',
  buttonDangerBg: '#1f1f1f',
  buttonDangerBorder: '#dc4446',
  buttonDangerText: '#ff7875',
};

function isDarkHost(): boolean {
  if (typeof document === 'undefined') return false;
  try {
    const bg = window.getComputedStyle(document.body).backgroundColor;
    const m = bg.match(/[\d.]+/g);
    if (!m || m.length < 3) return false;
    const [r, g, b] = m.map(Number);
    // Perceived brightness (ITU-R BT.601) — below the midpoint counts as dark.
    return (r * 299 + g * 587 + b * 114) / 1000 < 128;
  } catch {
    return false;
  }
}

// Not memoized: cheap (a single getComputedStyle call), and re-reading it
// per render means a Superset theme toggle takes effect immediately
// without needing this extension reloaded.
export function getTheme(): Palette {
  return isDarkHost() ? dark : light;
}

export function buttonStyle(variant: 'default' | 'primary' | 'danger' = 'default', disabled = false): CSSProperties {
  const t = getTheme();
  const base: CSSProperties = {
    padding: '4px 12px',
    borderRadius: 4,
    fontSize: 13,
    cursor: disabled ? 'not-allowed' : 'pointer',
    border: `1px solid ${t.buttonDefaultBorder}`,
    background: t.buttonDefaultBg,
    color: t.buttonDefaultText,
    opacity: disabled ? 0.5 : 1,
  };
  if (variant === 'primary') {
    return { ...base, background: t.buttonPrimaryBg, borderColor: t.buttonPrimaryBorder, color: t.buttonPrimaryText };
  }
  if (variant === 'danger') {
    return { ...base, background: t.buttonDangerBg, borderColor: t.buttonDangerBorder, color: t.buttonDangerText };
  }
  return base;
}

export function inputStyle(): CSSProperties {
  const t = getTheme();
  return {
    padding: '4px 8px',
    borderRadius: 4,
    border: `1px solid ${t.border}`,
    fontSize: 13,
    width: '100%',
    boxSizing: 'border-box',
    background: t.inputBg,
    color: t.inputText,
  };
}

export function labelStyle(): CSSProperties {
  return {
    display: 'flex',
    flexDirection: 'column',
    gap: 4,
    fontSize: 13,
    color: getTheme().text,
  };
}

export function helperTextStyle(): CSSProperties {
  return { fontSize: 12, color: getTheme().subtleText };
}

export function tableStyle(): CSSProperties {
  return { width: '100%', borderCollapse: 'collapse', fontSize: 13, color: getTheme().text };
}

export function thStyle(): CSSProperties {
  return { textAlign: 'left', padding: '8px 12px', borderBottom: `2px solid ${getTheme().headerBorder}`, fontWeight: 600 };
}

export function tdStyle(): CSSProperties {
  return { padding: '8px 12px', borderBottom: `1px solid ${getTheme().rowBorder}` };
}
