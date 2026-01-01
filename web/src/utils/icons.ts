// Icon mapping for environment types
export const ICONS: Record<string, string> = {
  terminal: '💻',
  desktop: '🖥️',
  code: '📝',
  book: '📚',
  box: '📦',
  web: '🌍',
  default: '🚀',
}

export function getIcon(icon?: string): string {
  return ICONS[icon || 'default'] || ICONS.default
}
