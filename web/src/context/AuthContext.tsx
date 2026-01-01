import { createContext, useContext, useState, useEffect, useCallback, ReactNode } from 'react'

const TOKEN_KEY = 'dploy_token'

interface AuthContextType {
  isAuthenticated: boolean
  username: string | null
  token: string | null
  login: () => void
  logout: () => void
  clearToken: () => void
}

const AuthContext = createContext<AuthContextType | null>(null)

function extractUsernameFromToken(token: string): string | null {
  try {
    const payload = JSON.parse(atob(token.split('.')[1]))
    return payload.name || payload.email || payload.sub || 'User'
  } catch {
    return null
  }
}

export function AuthProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(() => {
    return localStorage.getItem(TOKEN_KEY)
  })
  const [username, setUsername] = useState<string | null>(() => {
    const savedToken = localStorage.getItem(TOKEN_KEY)
    return savedToken ? extractUsernameFromToken(savedToken) : null
  })

  // Check for token in URL hash on mount (OAuth callback)
  useEffect(() => {
    const hash = window.location.hash
    if (hash && hash.startsWith('#token=')) {
      const newToken = hash.substring(7) // Remove '#token='
      localStorage.setItem(TOKEN_KEY, newToken)
      setToken(newToken)
      setUsername(extractUsernameFromToken(newToken))
      // Clean URL hash
      window.history.replaceState({}, document.title, window.location.pathname)
    }
  }, [])

  const login = useCallback(() => {
    const returnUrl = encodeURIComponent(window.location.pathname)
    window.location.href = `/auth/login?returnUrl=${returnUrl}`
  }, [])

  const logout = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY)
    setToken(null)
    setUsername(null)
    window.location.href = '/'
  }, [])

  const clearToken = useCallback(() => {
    localStorage.removeItem(TOKEN_KEY)
    setToken(null)
    setUsername(null)
  }, [])

  const value: AuthContextType = {
    isAuthenticated: !!token,
    username,
    token,
    login,
    logout,
    clearToken,
  }

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>
}

export function useAuth(): AuthContextType {
  const context = useContext(AuthContext)
  if (!context) {
    throw new Error('useAuth must be used within an AuthProvider')
  }
  return context
}
