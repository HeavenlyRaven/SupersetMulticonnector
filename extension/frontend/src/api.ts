import { authentication } from '@apache-superset/core';
import type { ApiError, SourceListResult, SourceSummary, SourceTypeInfo, TableInfo } from './types';

// Backend REST endpoints are namespaced /extensions/{publisher}/{name}/
// by the extensions framework (pre-1.0 — verify this exact prefix against
// the live Superset instance if requests 404; see README known
// limitations). publisher=acme, name=ch-federation (extension.json).
const API_BASE = '/extensions/acme/ch-federation';

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const isMutation = !!init.method && init.method !== 'GET';
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((init.headers as Record<string, string>) || {}),
  };
  if (isMutation) {
    // Every mutation gets a fresh CSRF token, per spec section 11.
    headers['X-CSRFToken'] = await authentication.getCSRFToken();
  }
  const resp = await fetch(`${API_BASE}${path}`, { ...init, headers, credentials: 'same-origin' });
  const body = await resp.json().catch(() => ({}) as Record<string, unknown>);
  if (!resp.ok) {
    const err: ApiError = {
      message: (body.message as string) || `request failed with status ${resp.status}`,
      hint: body.hint as string | undefined,
    };
    throw err;
  }
  return (('result' in body ? body.result : body) as T);
}

export const api = {
  sourceTypes: (): Promise<SourceTypeInfo[]> => request('/source-types'),
  listSources: (): Promise<SourceListResult> => request('/sources'),
  testSource: (name: string, type: string, params: Record<string, string>) =>
    request<{ ok: boolean; tableCount: number }>('/sources/test', {
      method: 'POST',
      body: JSON.stringify({ name, type, params }),
    }),
  addSource: (name: string, type: string, params: Record<string, string>) =>
    request<SourceSummary>('/sources', {
      method: 'POST',
      body: JSON.stringify({ name, type, params }),
    }),
  removeSource: (name: string) =>
    request<{ name: string; removed: boolean }>(`/sources/${encodeURIComponent(name)}`, {
      method: 'DELETE',
    }),
  sourceTables: (name: string): Promise<TableInfo[]> =>
    request(`/sources/${encodeURIComponent(name)}/tables`),
  probeSource: (name: string) =>
    request<{ name: string; reachable: boolean; tableCount: number }>(
      `/sources/${encodeURIComponent(name)}/probe`,
      { method: 'POST' },
    ),
};
