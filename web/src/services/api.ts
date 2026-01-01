import type {
  AvailableEnvironment,
  UserEnvironmentsResponse,
  RunEnvironmentResponse,
  EnvironmentStatusResponse,
  ExtendTTLResponse,
} from '../types'

const TOKEN_KEY = 'dploy_token'

interface ApiError {
  error: string
}

class ApiClient {
  private onUnauthorized?: () => void

  setOnUnauthorized(callback: () => void) {
    this.onUnauthorized = callback
  }

  private getToken(): string | null {
    return localStorage.getItem(TOKEN_KEY)
  }

  async request<T>(
    endpoint: string,
    options: RequestInit = {}
  ): Promise<T> {
    const token = this.getToken()
    const headers: HeadersInit = {
      'Content-Type': 'application/json',
      ...options.headers,
    }

    // Add Authorization header if token exists and endpoint requires auth
    if (token && !endpoint.includes('/available')) {
      (headers as Record<string, string>)['Authorization'] = `Bearer ${token}`
    }

    const response = await fetch(endpoint, {
      ...options,
      headers,
    })

    if (response.status === 401) {
      localStorage.removeItem(TOKEN_KEY)
      this.onUnauthorized?.()
      throw new Error('Authentication failed - please login')
    }

    if (!response.ok && response.status !== 204) {
      const error: ApiError = await response.json()
      throw new Error(error.error || 'Request failed')
    }

    if (response.status === 204) {
      return null as T
    }

    return response.json()
  }

  // Available environments (catalog)
  async getAvailableEnvironments(): Promise<AvailableEnvironment[]> {
    return this.request<AvailableEnvironment[]>('/api/environments/available')
  }

  // User's environments
  async getUserEnvironments(): Promise<UserEnvironmentsResponse> {
    return this.request<UserEnvironmentsResponse>('/api/environments')
  }

  // Create or get environment
  async runEnvironment(name: string): Promise<RunEnvironmentResponse> {
    return this.request<RunEnvironmentResponse>(`/api/run/${name}`)
  }

  // Get environment status
  async getEnvironmentStatus(name: string): Promise<EnvironmentStatusResponse> {
    return this.request<EnvironmentStatusResponse>(`/api/run/${name}/status`)
  }

  // Extend TTL
  async extendEnvironment(name: string): Promise<ExtendTTLResponse> {
    return this.request<ExtendTTLResponse>(`/api/run/${name}/extend`, {
      method: 'POST',
    })
  }

  // Delete environment
  async deleteEnvironment(name: string): Promise<void> {
    return this.request<void>(`/api/run/${name}`, {
      method: 'DELETE',
    })
  }
}

export const api = new ApiClient()
