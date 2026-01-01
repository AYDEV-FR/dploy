import { useState, useEffect } from 'react'
import { api } from '../services/api'
import { useToast } from '../context/ToastContext'
import { Hero, EnvCard } from '../components'
import { groupEnvironments, sortCategoryNames, slugify, capitalize } from '../utils/categories'
import type { AvailableEnvironment, CategoryGroup } from '../types'

export function Catalog() {
  const [environments, setEnvironments] = useState<AvailableEnvironment[]>([])
  const [loading, setLoading] = useState(true)
  const [error, setError] = useState<string | null>(null)
  const { showToast } = useToast()

  useEffect(() => {
    const loadEnvironments = async () => {
      try {
        setLoading(true)
        setError(null)
        const data = await api.getAvailableEnvironments()
        setEnvironments(data || [])
      } catch {
        setError('Failed to load templates')
        showToast('Failed to load templates', 'error')
      } finally {
        setLoading(false)
      }
    }

    loadEnvironments()
  }, [showToast])

  if (loading) {
    return (
      <>
        <Hero title="Templates Catalog" subtitle="Choose an environment template to deploy" />
        <div className="container">
          <main id="main-content" style={{ display: 'block' }}>
            <section className="section">
              <div className="catalog-layout">
                <div className="catalog-container">
                  <p className="loading">Loading...</p>
                </div>
              </div>
            </section>
          </main>
        </div>
      </>
    )
  }

  if (error) {
    return (
      <>
        <Hero title="Templates Catalog" subtitle="Choose an environment template to deploy" />
        <div className="container">
          <main id="main-content" style={{ display: 'block' }}>
            <section className="section">
              <div className="catalog-layout">
                <div className="catalog-container">
                  <p className="error-state">{error}</p>
                </div>
              </div>
            </section>
          </main>
        </div>
      </>
    )
  }

  if (environments.length === 0) {
    return (
      <>
        <Hero title="Templates Catalog" subtitle="Choose an environment template to deploy" />
        <div className="container">
          <main id="main-content" style={{ display: 'block' }}>
            <section className="section">
              <div className="catalog-layout">
                <div className="catalog-container">
                  <p className="empty-state">No templates available</p>
                </div>
              </div>
            </section>
          </main>
        </div>
      </>
    )
  }

  const groups = groupEnvironments(environments)
  const categoryNames = sortCategoryNames(Object.keys(groups))

  return (
    <>
      <Hero title="Templates Catalog" subtitle="Choose an environment template to deploy" />

      <div className="container">
        <main id="main-content" style={{ display: 'block' }}>
          <section className="section">
            <div className="catalog-layout">
              {/* Table of Contents */}
              <nav className="catalog-toc">
                <div className="toc-title">Categories</div>
                <ul className="toc-list">
                  {categoryNames.map((categoryName) => {
                    const group = groups[categoryName]
                    const displayName = categoryName === 'default' ? 'Other' : capitalize(categoryName)
                    const categorySlug = slugify(categoryName)
                    const subcategoryNames = Object.keys(group.subcategories).sort()

                    return (
                      <li key={categoryName} className="toc-category">
                        <a href={`#cat-${categorySlug}`} className="toc-category-link">
                          {displayName}
                        </a>
                        {subcategoryNames.length > 0 && (
                          <ul className="toc-subcategories">
                            {subcategoryNames.map((subcategoryName) => {
                              const subDisplayName = capitalize(subcategoryName)
                              const subSlug = slugify(subcategoryName)
                              return (
                                <li key={subcategoryName}>
                                  <a
                                    href={`#cat-${categorySlug}-${subSlug}`}
                                    className="toc-subcategory-link"
                                  >
                                    {subDisplayName}
                                  </a>
                                </li>
                              )
                            })}
                          </ul>
                        )}
                      </li>
                    )
                  })}
                </ul>
              </nav>

              {/* Environments Grid */}
              <div className="catalog-container">
                {categoryNames.map((categoryName) => {
                  const group: CategoryGroup = groups[categoryName]
                  const displayName = categoryName === 'default' ? 'Other' : capitalize(categoryName)
                  const categorySlug = slugify(categoryName)
                  const subcategoryNames = Object.keys(group.subcategories).sort()

                  return (
                    <div key={categoryName} id={`cat-${categorySlug}`} className="category-section">
                      <h3 className="category-title">{displayName}</h3>

                      {/* Direct items (no subcategory) */}
                      {group.direct.length > 0 && (
                        <div className="env-grid">
                          {group.direct.map((env) => (
                            <EnvCard key={env.name} env={env} />
                          ))}
                        </div>
                      )}

                      {/* Subcategories */}
                      {subcategoryNames.map((subcategoryName) => {
                        const items = group.subcategories[subcategoryName]
                        const subDisplayName = capitalize(subcategoryName)
                        const subSlug = slugify(subcategoryName)

                        return (
                          <div
                            key={subcategoryName}
                            id={`cat-${categorySlug}-${subSlug}`}
                            className="subcategory-section"
                          >
                            <h4 className="subcategory-title">{subDisplayName}</h4>
                            <div className="env-grid">
                              {items.map((env) => (
                                <EnvCard key={env.name} env={env} />
                              ))}
                            </div>
                          </div>
                        )
                      })}
                    </div>
                  )
                })}
              </div>
            </div>
          </section>
        </main>
      </div>
    </>
  )
}
