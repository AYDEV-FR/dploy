import { Link } from 'react-router-dom'
import type { AvailableEnvironment } from '../types'
import { getIcon } from '../utils/icons'
import { formatDuration } from '../utils/time'

interface EnvCardProps {
  env: AvailableEnvironment
}

export function EnvCard({ env }: EnvCardProps) {
  // Build TTL display for catalog
  const ttlBadge = env.isUnlimited ? (
    <span className="catalog-badge catalog-unlimited">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <path d="M18.178 8c5.096 0 5.096 8 0 8-5.095 0-7.133-8-12.739-8-4.585 0-4.585 8 0 8 5.606 0 7.644-8 12.74-8z" />
      </svg>
      Unlimited
    </span>
  ) : (
    <span className="catalog-badge catalog-ttl">
      <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
        <circle cx="12" cy="12" r="10" />
        <polyline points="12 6 12 12 16 14" />
      </svg>
      {formatDuration(env.ttl)}
    </span>
  )

  // Build extend info badge
  let extendBadge = null
  if (!env.isUnlimited && (env.extendTTL > 0 || env.maxExtends > 0)) {
    const extendDuration = env.extendTTL > 0 ? formatDuration(env.extendTTL) : ''
    const maxInfo = env.maxExtends > 0 ? `${env.maxExtends}x` : ''
    const extendText = extendDuration && maxInfo
      ? `+${extendDuration} (${maxInfo})`
      : extendDuration
        ? `+${extendDuration}`
        : maxInfo
          ? `Extend: ${maxInfo}`
          : ''

    if (extendText) {
      extendBadge = (
        <span className="catalog-badge catalog-extend">
          <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M12 2v4M12 18v4M4.93 4.93l2.83 2.83M16.24 16.24l2.83 2.83M2 12h4M18 12h4M4.93 19.07l2.83-2.83M16.24 7.76l2.83-2.83" />
          </svg>
          {extendText}
        </span>
      )
    }
  }

  return (
    <div className="env-card">
      <div className="env-card-icon">
        <span className="env-icon">{getIcon(env.icon)}</span>
      </div>
      <div className="env-card-content">
        <div className="env-name">{env.name}</div>
        <div className="env-description">{env.description}</div>
        <div className="env-card-badges">
          {ttlBadge}
          {extendBadge}
        </div>
      </div>
      <div className="env-card-action">
        <Link to={`/run/${env.name}`} className="btn-launch">
          Launch
          <svg width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
            <path d="M5 12h14M12 5l7 7-7 7" />
          </svg>
        </Link>
      </div>
    </div>
  )
}
