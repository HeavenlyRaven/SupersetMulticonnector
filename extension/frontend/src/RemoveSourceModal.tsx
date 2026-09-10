import React, { useState } from 'react';

import Modal from './Modal';
import { buttonStyle, inputStyle } from './styles';

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
    <Modal
      title={`Remove source "${name}"?`}
      onClose={onClose}
      footer={
        <>
          <button style={buttonStyle('default')} onClick={onClose}>
            Cancel
          </button>
          <button style={buttonStyle('danger', typed !== name || removing)} onClick={onConfirm} disabled={typed !== name || removing}>
            {removing ? 'Removing…' : 'Remove'}
          </button>
        </>
      }
    >
      <p>
        This detaches the database and named collection from ClickHouse. Type <strong>{name}</strong> to confirm.
      </p>
      <input style={inputStyle()} value={typed} onChange={(e) => setTyped(e.target.value)} />
    </Modal>
  );
}
