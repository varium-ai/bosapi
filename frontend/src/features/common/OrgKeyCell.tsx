import React from 'react';
import { ResourceName } from './ResourceName';

interface OrgKeyCellProps {
  orgId?: number | string | null;
  keyId?: number | string | null;
  orgName?: string;
  keyName?: string;
}

export const OrgKeyCell: React.FC<OrgKeyCellProps> = ({ orgId, keyId, orgName, keyName }) => (
  <div className="org-key-cell">
    <ResourceName kind="organizations" id={orgId} name={orgName} />
    <ResourceName kind="keys" id={keyId} name={keyName} />
  </div>
);
