export interface SourceField {
  name: string;
  label: string;
  type: 'text' | 'password' | 'number' | 'path';
  required: boolean;
  default?: string;
  helperText?: string;
}

export interface SourceTypeInfo {
  name: string;
  engine: string;
  hasCredentials: boolean;
  fields: SourceField[];
}

export interface SourceSummary {
  name: string;
  type: string;
  database: string;
  tableCount: number;
  host?: string;
}

export interface TableInfo {
  name: string;
  engine: string;
}

export interface SourceListResult {
  sources: SourceSummary[];
  // Lets the UI present as read-only for a non-privileged viewer; the
  // server enforces the same check independently on every mutating call
  // regardless of what this says (spec section 11).
  canManage: boolean;
}

export interface ApiError {
  message: string;
  hint?: string;
}

export function isApiError(e: unknown): e is ApiError {
  return typeof e === 'object' && e !== null && 'message' in e;
}
