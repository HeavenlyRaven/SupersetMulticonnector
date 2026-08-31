import React, { useMemo, useState } from 'react';
// @apache-superset/core/components re-exports Superset's own design
// system (Ant Design-derived) — verify these exact names against the
// live package if this fails to build; see README known limitations.
import { Button, Modal, Input, Select } from '@apache-superset/core/components';

import { api } from './api';
import type { SourceTypeInfo, ApiError } from './types';
import { isApiError } from './types';

interface Props {
  sourceTypes: SourceTypeInfo[];
  onClose: () => void;
  onAdded: () => void;
}

// defaultParamsFor seeds form state with each field's declared default
// (e.g. postgres port 5432, schema "public") — without this, an operator
// who accepts a displayed default without retyping it submits an empty
// value for that field and the request fails server-side.
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
    <Modal title="Add federated source" onCancel={onClose} visible footer={null}>
      <div style={{ display: 'flex', flexDirection: 'column', gap: 12 }}>
        <label>
          Name
          <Input value={name} onChange={(e: React.ChangeEvent<HTMLInputElement>) => { setName(e.target.value); setTestState('idle'); }} placeholder="fed_sales" />
        </label>

        <label>
          Type
          <Select
            value={typeName}
            onChange={(v: string) => {
              setTypeName(v);
              setParams(defaultParamsFor(sourceTypes.find((t) => t.name === v)));
              setTestState('idle');
            }}
            options={sourceTypes.map((t) => ({ value: t.name, label: t.name }))}
          />
        </label>

        {activeType?.fields.map((field) => (
          <label key={field.name}>
            {field.label}
            {field.required ? ' *' : ''}
            <Input
              type={field.type === 'password' ? 'password' : 'text'}
              value={params[field.name] ?? field.default ?? ''}
              onChange={(e: React.ChangeEvent<HTMLInputElement>) => setField(field.name, e.target.value)}
            />
            {field.helperText && <div style={{ fontSize: 12, opacity: 0.7 }}>{field.helperText}</div>}
          </label>
        ))}

        {testMessage && (
          <div role="status" style={{ color: testState === 'ok' ? 'green' : 'crimson' }}>
            {testMessage}
          </div>
        )}
        {error && <div role="alert" style={{ color: 'crimson' }}>{error.message}{error.hint ? ` (${error.hint})` : ''}</div>}

        <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end' }}>
          <Button onClick={onClose}>Cancel</Button>
          <Button onClick={handleTest} disabled={testState === 'testing' || !name || !typeName}>
            {testState === 'testing' ? 'Testing…' : 'Test connection'}
          </Button>
          <Button type="primary" onClick={handleAdd} disabled={!canAdd || saving}>
            {saving ? 'Adding…' : 'Add'}
          </Button>
        </div>
      </div>
    </Modal>
  );
}
