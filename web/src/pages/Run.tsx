import { useState, useEffect, useRef, useCallback } from 'react'
import { useParams } from 'react-router-dom'
import { useAuth } from '../context/AuthContext'
import { api } from '../services/api'

type Status = 'checking' | 'creating' | 'deploying' | 'ready' | 'error'

export function Run() {
  const { env: envName } = useParams<{ env: string }>()
  const { isAuthenticated, login, token } = useAuth()
  const [status, setStatus] = useState<Status>('checking')
  const [uuid, setUuid] = useState<string | null>(null)
  const [envUrl, setEnvUrl] = useState<string | null>(null)
  const [errorMessage, setErrorMessage] = useState<string | null>(null)
  const pollIntervalRef = useRef<number | null>(null)

  const stopPolling = useCallback(() => {
    if (pollIntervalRef.current) {
      clearInterval(pollIntervalRef.current)
      pollIntervalRef.current = null
    }
  }, [])

  const pollStatus = useCallback(async () => {
    if (!envName) return

    try {
      const statusResult = await api.getEnvironmentStatus(envName)
      console.log('Status:', statusResult.status)

      const statusLower = statusResult.status.toLowerCase()

      if (statusLower === 'healthy') {
        stopPolling()
        setStatus('ready')
        // Redirect after a short delay
        setTimeout(() => {
          if (envUrl) {
            window.location.href = envUrl
          }
        }, 1500)
      } else if (statusLower === 'degraded') {
        stopPolling()
        setStatus('error')
        setErrorMessage("L'environnement est en état dégradé. Vérifiez les logs ArgoCD.")
      } else if (statusLower === 'missing') {
        stopPolling()
        setStatus('error')
        setErrorMessage("L'environnement n'existe pas.")
      } else if (statusLower === 'deleting') {
        stopPolling()
        setStatus('error')
        setErrorMessage("L'environnement est en cours de suppression.")
      }
      // Otherwise keep polling (Progressing, Unknown, etc.)
    } catch (err) {
      console.error('Error checking status:', err)
    }
  }, [envName, envUrl, stopPolling])

  const startPolling = useCallback(() => {
    // Poll immediately
    pollStatus()
    // Then poll every 2 seconds
    pollIntervalRef.current = window.setInterval(pollStatus, 2000)
  }, [pollStatus])

  const createEnvironment = useCallback(async () => {
    if (!envName || !token) return

    setStatus('creating')

    try {
      const result = await api.runEnvironment(envName)
      console.log('Environment created:', result)

      setUuid(result.uuid)
      setEnvUrl(result.url)
      setStatus('deploying')

      // Start polling for status
      startPolling()
    } catch (err) {
      setStatus('error')
      setErrorMessage(err instanceof Error ? err.message : 'Erreur lors de la création')
    }
  }, [envName, token, startPolling])

  // Check authentication on mount
  useEffect(() => {
    if (!isAuthenticated) {
      login()
    } else {
      createEnvironment()
    }

    return () => {
      stopPolling()
    }
  }, [isAuthenticated, login, createEnvironment, stopPolling])

  const getStatusMessage = () => {
    switch (status) {
      case 'checking':
        return 'Vérification authentification...'
      case 'creating':
        return "Création de l'environnement..."
      case 'deploying':
        return uuid ? (
          <>
            Déploiement en cours...
            <br />
            <small>UUID: {uuid}</small>
          </>
        ) : (
          'Déploiement en cours...'
        )
      case 'ready':
        return '✅ Environnement prêt ! Redirection...'
      case 'error':
        return null
      default:
        return 'Chargement...'
    }
  }

  return (
    <div className="run-container">
      <h1>🚀 Dploy</h1>
      <div id="content">
        <div className="env-info">
          <div className="env-name">{envName}</div>
        </div>

        {status === 'error' ? (
          <>
            <div className="error-message">❌ {errorMessage}</div>
            <button onClick={() => window.location.reload()} className="btn btn-primary">
              Réessayer
            </button>
          </>
        ) : (
          <>
            <div className="spinner"></div>
            <div className="status">{getStatusMessage()}</div>
            <div className="progress-bar">
              <div className="progress-bar-fill"></div>
            </div>
          </>
        )}
      </div>

      <style>{`
        .run-container {
          max-width: 600px;
          margin: 50px auto;
          padding: 40px;
          background: var(--surface);
          border-radius: 12px;
          text-align: center;
        }

        .spinner {
          width: 50px;
          height: 50px;
          margin: 20px auto;
          border: 4px solid var(--border);
          border-top: 4px solid var(--primary);
          border-radius: 50%;
          animation: spin 1s linear infinite;
        }

        @keyframes spin {
          0% { transform: rotate(0deg); }
          100% { transform: rotate(360deg); }
        }

        .status {
          margin: 20px 0;
          font-size: 18px;
          color: var(--text-secondary);
        }

        .env-info {
          margin: 30px 0;
          padding: 20px;
          background: var(--bg);
          border-radius: 8px;
          border-left: 4px solid var(--primary);
        }

        .env-name {
          font-size: 24px;
          font-weight: 600;
          color: var(--primary);
          margin-bottom: 10px;
        }

        .error-message {
          color: var(--danger);
          margin: 20px 0;
          padding: 15px;
          background: rgba(239, 68, 68, 0.1);
          border-radius: 8px;
          border: 1px solid var(--danger);
        }

        .progress-bar {
          width: 100%;
          height: 4px;
          background: var(--border);
          border-radius: 2px;
          overflow: hidden;
          margin: 20px 0;
        }

        .progress-bar-fill {
          height: 100%;
          background: var(--primary);
          animation: progress 2s ease-in-out infinite;
        }

        @keyframes progress {
          0% { width: 0%; }
          50% { width: 70%; }
          100% { width: 100%; }
        }
      `}</style>
    </div>
  )
}
