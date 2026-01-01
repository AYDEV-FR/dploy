import { useState, useEffect, useCallback } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../services/api'
import { useToast } from '../context/ToastContext'
import { Hero, EnvListItem } from '../components'
import type { Environment } from '../types'

export function Environments() {
  const [environments, setEnvironments] = useState<Environment[]>([])
  const [count, setCount] = useState(0)
  const [limit, setLimit] = useState(0)
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const { showToast } = useToast()

  const loadEnvironments = useCallback(async () => {
    try {
      setLoading(true)
      setError(null)
      const data = await api.getUserEnvironments()
      setEnvironments(data.environments || [])
      setCount(data.count)
      setLimit(data.limit)
    } catch (err) {
      setError('Failed to load your environments')
      showToast('Failed to load environments', 'error')
    } finally {
      setLoading(false)
    }
  }, [showToast])

  useEffect(() => {
    loadEnvironments()
  }, [loadEnvironments])

  const handleOpen = (url: string) => {
    window.open(url, '_blank')
  }

  const handleExtend = async (name: string) => {
    try {
      const result = await api.extendEnvironment(name)
      const newExpiresAt = new Date(result.expiresAt)
      showToast(`Extended until ${newExpiresAt.toLocaleString()}`, 'success')
      await loadEnvironments()
    } catch {
      // Error already shown by API
    }
  }

  const handleDelete = async (name: string) => {
    if (!confirm(`Delete environment ${name}?`)) {
      return
    }

    try {
      await api.deleteEnvironment(name)
      showToast(`Environment ${name} deleted`, 'success')
      await loadEnvironments()
    } catch {
      // Error already shown by API
    }
  }

  const percentage = limit > 0 ? (count / limit) * 100 : 0
  const isNearLimit = percentage >= 80

  return (
    <>
      <Hero title="My Environments" subtitle="Manage your running development environments" />

      <div className="container">
        <main id="main-content" style={{ display: 'block' }}>
          <section className="section">
            <div className="section-header">
              <div className="section-title">
                <h2>Active Environments</h2>
                <p className="section-description">Your running development environments</p>
              </div>
              <div className="section-actions">
                <div className="env-counter">
                  <div className={`counter-content ${isNearLimit ? 'near-limit' : ''}`}>
                    <span className="counter-value">{count}</span>
                    <span className="counter-separator">/</span>
                    <span className="counter-limit">{limit}</span>
                    <span className="counter-label">environments</span>
                  </div>
                </div>
                <button onClick={loadEnvironments} className="btn-refresh" title="Refresh">
                  <svg width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                    <path d="M21.5 2v6h-6M2.5 22v-6h6M2 11.5a10 10 0 0 1 18.8-4.3M22 12.5a10 10 0 0 1-18.8 4.2" />
                  </svg>
                </button>
                <Link to="/catalog" className="btn-nav btn-small">
                  + New Environment
                </Link>
              </div>
            </div>

            <div className="env-list">
              {loading ? (
                <p className="loading">Loading...</p>
              ) : error ? (
                <p className="error-state">{error}</p>
              ) : environments.length === 0 ? (
                <div className="empty-state">
                  <p>No active environments yet</p>
                  <Link to="/catalog" className="btn-nav" style={{ display: 'inline-block', marginTop: '1rem' }}>
                    Browse Catalog
                  </Link>
                </div>
              ) : (
                environments.map((env) => (
                  <EnvListItem
                    key={env.uuid}
                    env={env}
                    onOpen={handleOpen}
                    onExtend={handleExtend}
                    onDelete={handleDelete}
                  />
                ))
              )}
            </div>
          </section>
        </main>
      </div>
    </>
  )
}
