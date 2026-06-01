export interface AvailableEnvironment {
  name: string;
  description: string;
  icon: string;
  category?: string;
  ttl: number;
  extendTTL: number;
  maxExtends: number;
  isUnlimited: boolean;
}

export type EnvironmentStatus =
  | 'Healthy'
  | 'Progressing'
  | 'Degraded'
  | 'Missing'
  | 'Unknown'
  | 'Deleting'
  | 'Pending';

/** How a connection is presented: a browser URL, or a copyable command. */
export type ConnectionType = 'web' | 'instructions';

export interface Environment {
  name: string;
  description: string;
  icon: string;
  uuid: string;
  url: string;
  status: EnvironmentStatus | string;
  expiresAt: string;
  isUnlimited: boolean;
  extendCount: number;
  maxExtends: number;
  owner?: string;
  shared?: boolean;
  connectionType?: ConnectionType | string;
  connectionMessage?: string;
}

export interface UserEnvironmentsResponse {
  environments: Environment[];
  count: number;
  limit: number;
}

export interface RunEnvironmentResponse {
  name: string;
  uuid: string;
  url: string;
  status: string;
  owner?: string;
  shared?: boolean;
  connectionType?: ConnectionType | string;
  connectionMessage?: string;
}

export interface EnvironmentStatusResponse {
  uuid: string;
  status: string;
  url: string;
  expiresAt: string;
  owner?: string;
  shared?: boolean;
  connectionType?: ConnectionType | string;
  connectionMessage?: string;
}

export interface ExtendTTLResponse {
  expiresAt: string;
}

/** UI feature flags fetched at bootstrap (public, no auth). */
export interface UIConfig {
  catalogEnabled: boolean;
  instancesEnabled: boolean;
  managerEnabled: boolean;
}

/** Authenticated requester's view of themselves (returned by GET /api/me). */
export interface Me {
  username: string;
  owner: string;
  admin: boolean;
}

/** One row of `GET /api/admin/instances` — shaped like `kubectl get dployinstance`. */
export interface AdminInstanceRow {
  name: string;
  template: string;
  owner: string;
  phase: string;
  url: string;
  expiresAt: string;
  createdAt: string;
  namespace: string;
  uuid: string;
  isUnlimited: boolean;
}

export interface AdminInstancesListResponse {
  instances: AdminInstanceRow[];
  count: number;
}

/** One row of `GET /api/admin/templates` — shaped like `kubectl get dploytemplate -o wide`. */
export interface AdminTemplateRow {
  name: string;
  displayName: string;
  method: string;
  enabled: boolean;
  visible: boolean;
  poolSize: number;
  available: number;
  claimed: number;
  chartType: string;
  chartRepo: string;
  chartRef: string;
  revision: string;
  createdAt: string;
}

export interface AdminTemplatesListResponse {
  templates: AdminTemplateRow[];
  count: number;
}
