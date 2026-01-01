// Available environment template from catalog
export interface AvailableEnvironment {
  name: string
  description: string
  icon: string
  category?: string
  ttl: number // in seconds, -1 for unlimited
  extendTTL: number // in seconds
  maxExtends: number
  isUnlimited: boolean
}

// User's active environment
export interface Environment {
  name: string
  description: string
  icon: string
  uuid: string
  url: string
  status: EnvironmentStatus
  expiresAt: string // ISO 8601 timestamp
  isUnlimited: boolean
  extendCount: number
  maxExtends: number
}

export type EnvironmentStatus =
  | 'Healthy'
  | 'Progressing'
  | 'Degraded'
  | 'Missing'
  | 'Unknown'
  | 'Deleting'
  | 'Syncing'
  | 'Pending'

// API response for user environments
export interface UserEnvironmentsResponse {
  environments: Environment[]
  count: number
  limit: number
}

// API response for creating/getting environment
export interface RunEnvironmentResponse {
  name: string
  uuid: string
  url: string
  status: string
  expiresAt: string
}

// API response for environment status
export interface EnvironmentStatusResponse {
  name: string
  status: string
  health: string
}

// API response for extending TTL
export interface ExtendTTLResponse {
  expiresAt: string
}

// Auth state
export interface AuthState {
  isAuthenticated: boolean
  username: string | null
  token: string | null
}

// Toast notification
export interface Toast {
  id: string
  message: string
  type: 'success' | 'error'
}

// Category grouping for catalog
export interface CategoryGroup {
  direct: AvailableEnvironment[]
  subcategories: Record<string, AvailableEnvironment[]>
}
