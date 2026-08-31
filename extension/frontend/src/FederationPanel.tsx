import React, { useCallback, useEffect, useState } from 'react';
import { Button, Table } from '@apache-superset/core/components';

import { api } from './api';
import AddSourceModal from './AddSourceModal';
import RemoveSourceModal from './RemoveSourceModal';
import type { ApiError, SourceSummary, SourceTypeInfo } from './types';
import { isApiError } from './types';

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

  const columns = [
    { title: 'Name', dataIndex: 'name', key: 'name' },
    { title: 'Type', dataIndex: 'type', key: 'type' },
    { title: 'Host', dataIndex: 'host', key: 'host' },
    { title: 'Tables', dataIndex: 'tableCount', key: 'tableCount' },
    {
      title: 'Health',
      key: 'health',
      render: (_: unknown, row: SourceSummary) => healthDot(health[row.name]),
    },
    {
      title: 'Last checked',
      key: 'lastChecked',
      render: (_: unknown, row: SourceSummary) => formatLastChecked(lastChecked[row.name]),
    },
    {
      title: 'Actions',
      key: 'actions',
      render: (_: unknown, row: SourceSummary) => (
        <div style={{ display: 'flex', gap: 8 }}>
          <Button size="small" onClick={() => probe(row.name)}>Probe</Button>
          {canManage && (
            <Button size="small" danger onClick={() => setRemoving(row.name)}>Remove</Button>
          )}
        </div>
      ),
    },
  ];

  return (
    <div style={{ padding: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <h3>Federated sources</h3>
        {canManage ? (
          <Button type="primary" onClick={() => setShowAdd(true)}>Add source</Button>
        ) : (
          <span style={{ opacity: 0.7 }} title="Your role does not include manage access for federated sources">
            Read-only
          </span>
        )}
      </div>

      {error && (
        <div role="alert" style={{ color: 'crimson', marginBottom: 12 }}>
          {error.message}{error.hint ? ` — ${error.hint}` : ''}
        </div>
      )}

      <Table loading={loading} dataSource={sources} columns={columns} rowKey="name" />

      {showAdd && (
        <AddSourceModal
          sourceTypes={sourceTypes}
          onClose={() => setShowAdd(false)}
          onAdded={() => { setShowAdd(false); refresh(); }}
        />
      )}

      {removing && (
        <RemoveSourceModal
          name={removing}
          removing={removeBusy}
          onClose={() => setRemoving(null)}
          onConfirm={confirmRemove}
        />
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
