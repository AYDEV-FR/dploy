interface StatusBadgeProps {
  status: string
}

export function StatusBadge({ status }: StatusBadgeProps) {
  const statusClass = status.toLowerCase()

  return (
    <span className={`env-status status-${statusClass}`}>
      {status}
    </span>
  )
}
