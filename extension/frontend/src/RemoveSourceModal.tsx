import React, { useState } from 'react';
import { Button, Modal, Input } from '@apache-superset/core/components';

interface Props {
  name: string;
  onClose: () => void;
  onConfirm: () => void;
  removing: boolean;
}

// Remove requires typed confirmation (spec section 11): the operator must
// type the exact source name before the button enables, so a stray click
// can't drop a live federated source.
export default function RemoveSourceModal({ name, onClose, onConfirm, removing }: Props) {
  const [typed, setTyped] = useState('');
  return (
    <Modal title={`Remove source "${name}"?`} onCancel={onClose} visible footer={null}>
      <p>This detaches the database and named collection from ClickHouse. Type <strong>{name}</strong> to confirm.</p>
      <Input value={typed} onChange={(e: React.ChangeEvent<HTMLInputElement>) => setTyped(e.target.value)} />
      <div style={{ display: 'flex', gap: 8, justifyContent: 'flex-end', marginTop: 12 }}>
        <Button onClick={onClose}>Cancel</Button>
        <Button danger onClick={onConfirm} disabled={typed !== name || removing}>
          {removing ? 'Removing…' : 'Remove'}
        </Button>
      </div>
    </Modal>
  );
}
