import { authentication } from '@apache-superset/core';
import type { ApiError, SourceListResult, SourceSummary, SourceTypeInfo, SqliteFileInfo, SqliteUploadResult, TableInfo } from './types';

// Backend REST endpoints are namespaced /extensions/{publisher}/{name}/
// by the extensions framework (pre-1.0 — verify this exact prefix against
// the live Superset instance if requests 404; see README known
// limitations). publisher=acme, name=ch-federation (extension.json).
const API_BASE = '/extensions/acme/ch-federation';

async function parseResponse<T>(resp: Response): Promise<T> {
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

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const isMutation = !!init.method && init.method !== 'GET';
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
    ...((init.headers as Record<string, string>) || {}),
  };
  if (isMutation) {
    // Every mutation gets a fresh CSRF token, per spec section 11.
    // getCSRFToken() is typed as returning string | undefined in the real
    // package (live-verified) — an absent token just means no header gets
    // set, and the request fails with Superset's own CSRF error, which is
    // clearer than a client-side crash here.
    const token = await authentication.getCSRFToken();
    if (token) headers['X-CSRFToken'] = token;
  }
  const resp = await fetch(`${API_BASE}${path}`, { ...init, headers, credentials: 'same-origin' });
  return parseResponse<T>(resp);
}

// Separate from request(): a file upload must NOT set Content-Type itself
// — fetch only generates the correct `multipart/form-data; boundary=...`
// header when it's left unset and the body is a FormData instance.
async function uploadRequest<T>(path: string, formData: FormData): Promise<T> {
  const headers: Record<string, string> = {};
  const token = await authentication.getCSRFToken();
  if (token) headers['X-CSRFToken'] = token;
  const resp = await fetch(`${API_BASE}${path}`, { method: 'POST', headers, body: formData, credentials: 'same-origin' });
  return parseResponse<T>(resp);
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
  listSqliteFiles: (): Promise<SqliteFileInfo[]> => request('/sources/sqlite-files'),
  // overwrite=true is the "Replace" action: fedctl refuses to touch an
  // existing file otherwise, so a plain "Upload new file…" can't
  // accidentally clobber one under the same name.
  uploadSqliteFile: (file: File, name?: string, overwrite?: boolean): Promise<SqliteUploadResult> => {
    const formData = new FormData();
    formData.append('file', file);
    if (name) formData.append('name', name);
    if (overwrite) formData.append('overwrite', 'true');
    return uploadRequest<SqliteUploadResult>('/sources/sqlite-files', formData);
  },
};
