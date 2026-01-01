import { Link, useLocation } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'

export function Navbar() {
  const location = useLocation()
  const { isAuthenticated, username, login, logout } = useAuth()

  const isActive = (path: string) => location.pathname === path

  return (
    <nav className="navbar">
      <div className="nav-container">
        <div className="nav-brand">
          <Link to="/" className="brand-link">
            <span className="brand-icon">🚀</span>
            <span className="brand-name">Dploy.dev</span>
          </Link>
        </div>
        <div className="nav-links">
          <Link to="/" className={`nav-link ${isActive('/') ? 'active' : ''}`}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <rect x="3" y="3" width="7" height="7" />
              <rect x="14" y="3" width="7" height="7" />
              <rect x="14" y="14" width="7" height="7" />
              <rect x="3" y="14" width="7" height="7" />
            </svg>
            My Environments
          </Link>
          <Link to="/catalog" className={`nav-link ${isActive('/catalog') ? 'active' : ''}`}>
            <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
              <path d="M4 19.5A2.5 2.5 0 0 1 6.5 17H20" />
              <path d="M6.5 2H20v20H6.5A2.5 2.5 0 0 1 4 19.5v-15A2.5 2.5 0 0 1 6.5 2z" />
            </svg>
            Catalog
          </Link>
        </div>
        <div className="nav-menu">
          {!isAuthenticated ? (
            <div id="login-form">
              <button onClick={login} className="btn-nav">
                Login with SSO
              </button>
            </div>
          ) : (
            <div id="user-info" style={{ display: 'flex' }}>
              <div className="user-badge">
                <span className="user-icon">👤</span>
                <span className="username">{username}</span>
              </div>
              <button onClick={logout} className="btn-logout">
                Logout
              </button>
            </div>
          )}
        </div>
      </div>
    </nav>
  )
}
