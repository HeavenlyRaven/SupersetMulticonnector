import React, { useMemo, useState } from 'react';
import { components } from '@apache-superset/core';

import { api } from './api';
import Modal from './Modal';
import SqliteFileField from './SqliteFileField';
import { buttonStyle, helperTextStyle, inputStyle, labelStyle } from './styles';
import type { SourceTypeInfo, ApiError } from './types';
import { isApiError } from './types';

const { Alert } = components;

interface Props {
  sourceTypes: SourceTypeInfo[];
  onClose: () => void;
  onAdded: () => void;
}

// Seeds form state with each field's declared default (e.g. postgres port
// 5432, schema "public") — without this, an operator who accepts a
// displayed default without retyping it submits an empty value instead.
function defaultParamsFor(ty: SourceTypeInfo | undefined): Record<string, string> {
  const out: Record<string, string> = {};
  ty?.fields.forEach((f) => {
    if (f.default) out[f.name] = f.default;
  });
  return out;
}

export default function AddSourceModal({ sourceTypes, onClose, onAdded }: Props) {
  const [typeName, setTypeName] = useState(sourceTypes[0]?.name ?? '');
  const [name, setName] = useState('');
  const [params, setParams] = useState<Record<string, string>>(() => defaultParamsFor(sourceTypes[0]));
  const [testState, setTestState] = useState<'idle' | 'testing' | 'ok' | 'failed'>('idle');
  const [testMessage, setTestMessage] = useState<string>('');
  const [testedSignature, setTestedSignature] = useState<string>('');
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState<ApiError | null>(null);

  const activeType = useMemo(() => sourceTypes.find((t) => t.name === typeName), [sourceTypes, typeName]);
  const signature = useMemo(() => JSON.stringify({ typeName, name, params }), [typeName, name, params]);
  // Test-connection must succeed before Add enables (spec section 11) —
  // and any edit after a passing test invalidates it, so a stale pass
  // can't be reused against different parameters.
  const canAdd = testState === 'ok' && testedSignature === signature;

  function setField(fieldName: string, value: string) {
    setParams((p) => ({ ...p, [fieldName]: value }));
    setTestState('idle');
  }

  async function handleTest() {
    setTestState('testing');
    setError(null);
    try {
      const result = await api.testSource(name, typeName, params);
      setTestState('ok');
      setTestedSignature(signature);
      setTestMessage(`Reachable — ${result.tableCount} table(s) visible.`);
    } catch (e) {
      setTestState('failed');
      if (isApiError(e)) {
        setTestMessage(e.hint ? `${e.message} (${e.hint})` : e.message);
      } else {
        setTestMessage('Connection test failed.');
      }
    }
  }

  async function handleAdd() {
    setSaving(true);
    setError(null);
    try {
      await api.addSource(name, typeName, params);
      onAdded();
    } catch (e) {
      setError(isApiError(e) ? e : { message: 'Failed to add source.' });
    } finally {
      setSaving(false);
    }
  }

  return (
    <Modal
      title="Add federated source"
      onClose={onClose}
      footer={
        <>
          <button style={buttonStyle('default')} onClick={onClose}>
            Cancel
          </button>
          <button
            style={buttonStyle('default', testState === 'testing' || !name || !typeName)}
            onClick={handleTest}
            disabled={testState === 'testing' || !name || !typeName}
          >
            {testState === 'testing' ? 'Testing…' : 'Test connection'}
          </button>
          <button style={buttonStyle('primary', !canAdd || saving)} onClick={handleAdd} disabled={!canAdd || saving}>
            {saving ? 'Adding…' : 'Add'}
          </button>
        </>
      }
    >
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <label style={labelStyle()}>
          Name
          <input
            style={inputStyle()}
            value={name}
            onChange={(e) => {
              setName(e.target.value);
              setTestState('idle');
            }}
            placeholder="fed_sales"
          />
        </label>

        <label style={labelStyle()}>
          Type
          <select
            style={inputStyle()}
            value={typeName}
            onChange={(e) => {
              setTypeName(e.target.value);
              setParams(defaultParamsFor(sourceTypes.find((t) => t.name === e.target.value)));
              setTestState('idle');
            }}
          >
            {sourceTypes.map((t) => (
              <option key={t.name} value={t.name}>
                {t.name}
              </option>
            ))}
          </select>
        </label>

        {activeType?.fields.map((field) => (
          <label style={labelStyle()} key={field.name}>
            {field.label}
            {field.required ? ' *' : ''}
            {field.type === 'path' ? (
              <SqliteFileField value={params[field.name] ?? ''} onChange={(v) => setField(field.name, v)} />
            ) : (
              <input
                style={inputStyle()}
                type={field.type === 'password' ? 'password' : 'text'}
                value={params[field.name] ?? field.default ?? ''}
                onChange={(e) => setField(field.name, e.target.value)}
              />
            )}
            {field.helperText && <div style={helperTextStyle()}>{field.helperText}</div>}
          </label>
        ))}

        {testMessage && (
          <Alert type={testState === 'ok' ? 'success' : 'error'} showIcon>
            {testMessage}
          </Alert>
        )}
        {error && (
          <Alert type="error" showIcon>
            {error.message}
            {error.hint ? ` (${error.hint})` : ''}
          </Alert>
        )}
      </div>
    </Modal>
  );
}
