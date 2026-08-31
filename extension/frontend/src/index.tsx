import React from 'react';
import { views } from '@apache-superset/core';

import FederationPanel from './FederationPanel';

// Superset always loads an extension by requesting the ./index module
// from its Module Federation container — this file must be exactly what
// webpack.config.js exposes as './index' (spec section 10/quick-start).
//
// No other SQL Lab-adjacent contribution point exists for a full admin
// panel today (only sqllab.leftSidebar / .editor / .rightSidebar /
// .panels — see README known limitations); sqllab.panels is the closest
// fit for a source management table.
views.registerView(
  { id: 'acme.ch-federation.panel', name: 'Federated sources' },
  'sqllab.panels',
  () => <FederationPanel />,
);
