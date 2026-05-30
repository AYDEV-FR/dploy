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
}
