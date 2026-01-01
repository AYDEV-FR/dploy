import type { AvailableEnvironment, CategoryGroup } from '../types'

// Parse category string into { category, subcategory }
export function parseCategory(categoryStr?: string): { category: string; subcategory: string | null } {
  if (!categoryStr) {
    return { category: 'default', subcategory: null }
  }
  const parts = categoryStr.split(',')
  return {
    category: parts[0] || 'default',
    subcategory: parts[1] || null,
  }
}

// Group environments by category and subcategory
export function groupEnvironments(envs: AvailableEnvironment[]): Record<string, CategoryGroup> {
  const groups: Record<string, CategoryGroup> = {}

  envs.forEach((env) => {
    const { category, subcategory } = parseCategory(env.category)

    if (!groups[category]) {
      groups[category] = { direct: [], subcategories: {} }
    }

    if (subcategory) {
      if (!groups[category].subcategories[subcategory]) {
        groups[category].subcategories[subcategory] = []
      }
      groups[category].subcategories[subcategory].push(env)
    } else {
      groups[category].direct.push(env)
    }
  })

  return groups
}

// Sort category names: 'default' last, others alphabetically
export function sortCategoryNames(names: string[]): string[] {
  return [...names].sort((a, b) => {
    if (a === 'default') return 1
    if (b === 'default') return -1
    return a.localeCompare(b)
  })
}

// Generate slug for anchor IDs
export function slugify(text: string): string {
  return text.toLowerCase().replace(/[^a-z0-9]+/g, '-').replace(/(^-|-$)/g, '')
}

// Capitalize first letter
export function capitalize(text: string): string {
  return text.charAt(0).toUpperCase() + text.slice(1)
}
