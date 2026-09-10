import React, { useCallback, useEffect, useState } from 'react';
import { components } from '@apache-superset/core';

import { api } from './api';
import AddSourceModal from './AddSourceModal';
import RemoveSourceModal from './RemoveSourceModal';
import { buttonStyle, tableStyle, thStyle, tdStyle } from './styles';
import type { ApiError, SourceSummary, SourceTypeInfo } from './types';
import { isApiError } from './types';

const { Alert } = components;

type HealthState = 'unknown' | 'checking' | 'healthy' | 'unhealthy';

export default function FederationPanel() {
  const [sources, setSources] = useState<SourceSummary[]>([]);
  const [canManage, setCanManage] = useState(false);
  const [sourceTypes, setSourceTypes] = useState<SourceTypeInfo[]>([]);
  const [health, setHealth] = useState<Record<string, HealthState>>({});
  const [lastChecked, setLastChecked] = useState<Record<string, Date>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<ApiError | null>(null);
  const [showAdd, setShowAdd] = useState(false);
  const [removing, setRemoving] = useState<string | null>(null);
  const [removeBusy, setRemoveBusy] = useState(false);

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(null);
    try {
      const [list, types] = await Promise.all([api.listSources(), api.sourceTypes()]);
      setSources(list.sources);
      setCanManage(list.canManage);
      setSourceTypes(types);
    } catch (e) {
      // A 403 here means this user lacks even read access; the server
      // enforces that independently of anything the UI does or hides.
      setError(isApiError(e) ? e : { message: 'Failed to load sources.' });
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    refresh();
  }, [refresh]);

  async function probe(name: string) {
    setHealth((h) => ({ ...h, [name]: 'checking' }));
    try {
      const result = await api.probeSource(name);
      setHealth((h) => ({ ...h, [name]: result.reachable ? 'healthy' : 'unhealthy' }));
    } catch {
      setHealth((h) => ({ ...h, [name]: 'unhealthy' }));
    } finally {
      setLastChecked((lc) => ({ ...lc, [name]: new Date() }));
    }
  }

  async function confirmRemove() {
    if (!removing) return;
    setRemoveBusy(true);
    try {
      await api.removeSource(removing);
      setRemoving(null);
      await refresh();
    } catch (e) {
      setError(isApiError(e) ? e : { message: 'Failed to remove source.' });
    } finally {
      setRemoveBusy(false);
    }
  }

  return (
    <div style={{ padding: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <h3 style={{ margin: 0 }}>Federated sources</h3>
        {canManage ? (
          <button style={buttonStyle('primary')} onClick={() => setShowAdd(true)}>
            Add source
          </button>
        ) : (
          <span style={{ opacity: 0.7, fontSize: 13 }} title="Your role does not include manage access for federated sources">
            Read-only
          </span>
        )}
      </div>

      {error && (
        <div style={{ marginBottom: 12 }}>
          <Alert type="error" showIcon>
            {error.message}
            {error.hint ? ` — ${error.hint}` : ''}
          </Alert>
        </div>
      )}

      {loading ? (
        <p>Loading…</p>
      ) : sources.length === 0 ? (
        <p style={{ opacity: 0.7 }}>No federated sources yet.</p>
      ) : (
        <table style={tableStyle()}>
          <thead>
            <tr>
              <th style={thStyle()}>Name</th>
              <th style={thStyle()}>Type</th>
              <th style={thStyle()}>Host</th>
              <th style={thStyle()}>Tables</th>
              <th style={thStyle()}>Health</th>
              <th style={thStyle()}>Last checked</th>
              <th style={thStyle()}>Actions</th>
            </tr>
          </thead>
          <tbody>
            {sources.map((row) => (
              <tr key={row.name}>
                <td style={tdStyle()}>{row.name}</td>
                <td style={tdStyle()}>{row.type}</td>
                <td style={tdStyle()}>{row.host || <span style={{ opacity: 0.5 }}>— (local file)</span>}</td>
                <td style={tdStyle()}>{row.tableCount}</td>
                <td style={tdStyle()}>{healthDot(health[row.name])}</td>
                <td style={tdStyle()}>{formatLastChecked(lastChecked[row.name])}</td>
                <td style={tdStyle()}>
                  <div style={{ display: 'flex', gap: 8 }}>
                    <button style={buttonStyle('default')} onClick={() => probe(row.name)}>
                      Probe
                    </button>
                    {canManage && (
                      <button style={buttonStyle('danger')} onClick={() => setRemoving(row.name)}>
                        Remove
                      </button>
                    )}
                  </div>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      )}

      {showAdd && (
        <AddSourceModal
          sourceTypes={sourceTypes}
          onClose={() => setShowAdd(false)}
          onAdded={() => {
            setShowAdd(false);
            refresh();
          }}
        />
      )}

      {removing && (
        <RemoveSourceModal name={removing} removing={removeBusy} onClose={() => setRemoving(null)} onConfirm={confirmRemove} />
      )}
    </div>
  );
}

function healthDot(state: HealthState | undefined) {
  const color = { unknown: '#999', checking: '#1890ff', healthy: '#52c41a', unhealthy: '#f5222d' }[state ?? 'unknown'];
  const label = { unknown: 'not checked', checking: 'checking…', healthy: 'healthy', unhealthy: 'unreachable' }[state ?? 'unknown'];
  return (
    <span title={label} style={{ display: 'inline-flex', alignItems: 'center', gap: 6 }}>
      <span style={{ width: 8, height: 8, borderRadius: '50%', background: color, display: 'inline-block' }} />
      {label}
    </span>
  );
}

function formatLastChecked(when: Date | undefined) {
  if (!when) return <span style={{ opacity: 0.6 }}>never</span>;
  return <span title={when.toISOString()}>{when.toLocaleTimeString()}</span>;
}
