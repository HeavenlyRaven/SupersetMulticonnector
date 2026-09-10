import React, { useEffect, useRef, useState } from 'react';
import { components } from '@apache-superset/core';

import { api } from './api';
import { buttonStyle, helperTextStyle, inputStyle } from './styles';
import type { ApiError, SqliteFileInfo } from './types';
import { isApiError } from './types';

const { Alert } = components;

interface Props {
  value: string;
  onChange: (path: string) => void;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KB`;
  return `${(n / (1024 * 1024)).toFixed(1)} MB`;
}

// Replaces a free-text "type the path yourself" field: a dropdown of files
// already sitting in the ClickHouse user_files volume, an Upload button for
// a genuinely new file, and — since SQLite has no live connection to
// re-read the way postgres/mysql sources do (a SQLite database is a local
// file snapshot, not a network service) — a Replace button that re-picks a
// file from disk and overwrites the CURRENTLY SELECTED name's bytes in
// place. That keeps the ClickHouse database definition (which points at
// this exact filename) untouched while refreshing its data.
export default function SqliteFileField({ value, onChange }: Props) {
  const [files, setFiles] = useState<SqliteFileInfo[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState<string | null>(null);
  const [busy, setBusy] = useState<'idle' | 'uploading' | 'replacing'>('idle');
  const [opError, setOpError] = useState<ApiError | null>(null);
  const [statusMessage, setStatusMessage] = useState<string | null>(null);
  const uploadInputRef = useRef<HTMLInputElement>(null);
  const replaceInputRef = useRef<HTMLInputElement>(null);

  const refresh = React.useCallback(async () => {
    setLoading(true);
    setListError(null);
    try {
      setFiles(await api.listSqliteFiles());
    } catch (e) {
      setListError(isApiError(e) ? e.message : 'Failed to list uploaded files.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function handleUploadChosen(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = ''; // let the same file be re-selected later if needed
    if (!file) return;
    setBusy('uploading');
    setOpError(null);
    setStatusMessage(null);
    try {
      const result = await api.uploadSqliteFile(file);
      setFiles((prev) => [...prev.filter((f) => f.name !== result.name), result].sort((a, b) => a.name.localeCompare(b.name)));
      onChange(result.name);
      setStatusMessage(`Uploaded ${result.name}.`);
    } catch (err) {
      setOpError(isApiError(err) ? err : { message: 'Upload failed.' });
    } finally {
      setBusy('idle');
    }
  }

  async function handleReplaceChosen(e: React.ChangeEvent<HTMLInputElement>) {
    const file = e.target.files?.[0];
    e.target.value = '';
    if (!file || !value) return;
    setBusy('replacing');
    setOpError(null);
    setStatusMessage(null);
    try {
      // Target name is the currently selected file, not the picked file's
      // own name — the point of Replace is refreshing the same source's
      // data without changing what its ClickHouse database points at.
      const result = await api.uploadSqliteFile(file, value, true);
      setFiles((prev) => prev.map((f) => (f.name === result.name ? result : f)));
      setStatusMessage(`Replaced ${result.name} — the new bytes are on disk now.`);
    } catch (err) {
      setOpError(isApiError(err) ? err : { message: 'Replace failed.' });
    } finally {
      setBusy('idle');
    }
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
      <select
        style={inputStyle()}
        value={value}
        onChange={(e) => {
          onChange(e.target.value);
          setStatusMessage(null);
        }}
      >
        <option value="">
          {loading ? 'Loading files…' : files.length ? 'Select a file…' : 'No files uploaded yet'}
        </option>
        {files.map((f) => (
          <option key={f.name} value={f.name}>
            {f.name} ({formatBytes(f.sizeBytes)})
          </option>
        ))}
      </select>
      <div style={{ display: 'flex', gap: 8 }}>
        <button
          type="button"
          style={buttonStyle('default', busy !== 'idle')}
          onClick={() => uploadInputRef.current?.click()}
          disabled={busy !== 'idle'}
        >
          {busy === 'uploading' ? 'Uploading…' : 'Upload new file…'}
        </button>
        <input ref={uploadInputRef} type="file" style={{ display: 'none' }} onChange={handleUploadChosen} />

        <button
          type="button"
          style={buttonStyle('default', busy !== 'idle' || !value)}
          onClick={() => replaceInputRef.current?.click()}
          disabled={busy !== 'idle' || !value}
          title={value ? `Pick a file from disk to overwrite "${value}" with` : 'Select a file above first'}
        >
          {busy === 'replacing' ? 'Replacing…' : 'Replace selected file…'}
        </button>
        <input ref={replaceInputRef} type="file" style={{ display: 'none' }} onChange={handleReplaceChosen} />
      </div>
      {listError && <div style={helperTextStyle()}>{listError}</div>}
      {statusMessage && (
        <Alert type="success" showIcon>
          {statusMessage}
        </Alert>
      )}
      {opError && (
        <Alert type="error" showIcon>
          {opError.message}
          {opError.hint ? ` (${opError.hint})` : ''}
        </Alert>
      )}
    </div>
  );
}
