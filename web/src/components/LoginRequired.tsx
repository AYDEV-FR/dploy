import { useAuth } from '../context/AuthContext'

export function LoginRequired() {
  const { login } = useAuth()

  return (
    <div className="login-required" style={{ display: 'flex' }}>
      <div className="login-card">
        <div className="login-icon">🔐</div>
        <h2>Authentication Required</h2>
        <p>Please sign in with your SSO account to access Dploy</p>
        <button onClick={login} className="btn-login-large">
          Login with SSO
        </button>
      </div>
    </div>
  )
}
