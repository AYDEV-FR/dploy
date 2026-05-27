import type { Environment } from '../types'
import { getIcon } from '../utils/icons'
import { getTimeLeft, isExpiringSoon } from '../utils/time'
import { StatusBadge } from './StatusBadge'

interface EnvListItemProps {
  env: Environment
  onOpen: (url: string) => void
  onExtend: (name: string) => void
  onDelete: (name: string) => void
}

export function EnvListItem({ env, onOpen, onExtend, onDelete }: EnvListItemProps) {
  // Build TTL display
  let ttlDisplay = null
  let extendButton = null

  if (env.isUnlimited) {
    ttlDisplay = (
      <span className="meta-badge meta-unlimited">
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <path d="M12 6v6l4 2" />
        </svg>
        Unlimited
      </span>
    )
  } else {
    const timeLeft = getTimeLeft(env.expiresAt)
    const expiringSoon = isExpiringSoon(env.expiresAt)

    ttlDisplay = (
      <span className={`meta-badge ${expiringSoon ? 'meta-warning' : ''}`}>
        <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
        {timeLeft}
      </span>
    )

    // Build extend info
    let extendInfo = ''
    if (env.maxExtends > 0) {
      const remaining = env.maxExtends - env.extendCount
      extendInfo = `(${remaining}/${env.maxExtends} left)`
    } else if (env.extendCount > 0) {
      extendInfo = `(${env.extendCount}x extended)`
    }

    // Check if can extend
    const canExtend = env.maxExtends <= 0 || env.extendCount < env.maxExtends

    extendButton = (
      <button
        onClick={() => onExtend(env.name)}
        className={`btn-icon-action ${!canExtend ? 'btn-disabled' : ''}`}
        title={`Extend ${extendInfo}`}
        disabled={!canExtend}
      >
        <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
      </button>
    )
  }

  return (
    <div className="env-list-item">
      <div className="env-list-main">
        <div className="env-list-icon">{getIcon(env.icon)}</div>
        <div className="env-list-info">
          <div className="env-list-header">
            <span className="env-list-name">{env.name}</span>
            <StatusBadge status={env.status} />
          </div>
          <div className="env-list-description">{env.description}</div>
          <a href={env.url} target="_blank" rel="noopener noreferrer" className="env-list-url">
            {env.url}
          </a>
          <div className="env-list-meta">
            <span className="meta-badge">
              <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <rect x="3" y="3" width="18" height="18" rx="2" ry="2" />
                <line x1="9" y1="3" x2="9" y2="21" />
              </svg>
              {env.uuid}
            </span>
            {ttlDisplay}
            {env.shared && env.owner && (
              <span className="meta-badge meta-shared" title={`Shared environment — owned by ${env.owner}`}>
                <svg width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
                  <circle cx="9" cy="7" r="4" />
                  <path d="M23 21v-2a4 4 0 0 0-3-3.87M16 3.13a4 4 0 0 1 0 7.75" />
                </svg>
                owned by {env.owner}
              </span>
            )}
          </div>
        </div>
      </div>
      <div className="env-list-actions">
        <button onClick={() => onOpen(env.url)} className="btn-icon-action" title="Open">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" />
            <polyline points="15 3 21 3 21 9" />
            <line x1="10" y1="14" x2="21" y2="3" />
          </svg>
        </button>
        {extendButton}
        <button onClick={() => onDelete(env.name)} className="btn-icon-action btn-danger-icon" title="Delete">
          <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <polyline points="3 6 5 6 21 6" />
            <path d="M19 6v14a2 2 0 0 1-2 2H7a2 2 0 0 1-2-2V6m3 0V4a2 2 0 0 1 2-2h4a2 2 0 0 1 2 2v2" />
          </svg>
        </button>
      </div>
    </div>
  )
}
