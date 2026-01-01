// Calculate time left from expiration date
export function getTimeLeft(expiresAt: Date | string): string {
  const expiration = typeof expiresAt === 'string' ? new Date(expiresAt) : expiresAt
  const now = new Date()
  const diff = expiration.getTime() - now.getTime()

  if (diff < 0) {
    return 'Expired'
  }

  const hours = Math.floor(diff / (1000 * 60 * 60))
  const minutes = Math.floor((diff % (1000 * 60 * 60)) / (1000 * 60))

  if (hours > 0) {
    return `${hours}h ${minutes}m`
  }
  return `${minutes}m`
}

// Format duration from seconds to human readable
export function formatDuration(seconds: number): string {
  if (seconds < 0) return 'Unlimited'
  if (seconds === 0) return 'Default'

  const days = Math.floor(seconds / 86400)
  const hours = Math.floor((seconds % 86400) / 3600)
  const minutes = Math.floor((seconds % 3600) / 60)

  if (days > 0) {
    return hours > 0 ? `${days}d ${hours}h` : `${days}d`
  }
  if (hours > 0) {
    return minutes > 0 ? `${hours}h ${minutes}m` : `${hours}h`
  }
  return `${minutes}m`
}

// Check if environment is expiring soon (< 30 minutes)
export function isExpiringSoon(expiresAt: Date | string): boolean {
  const expiration = typeof expiresAt === 'string' ? new Date(expiresAt) : expiresAt
  const diff = expiration.getTime() - new Date().getTime()
  return diff < 30 * 60 * 1000
}
