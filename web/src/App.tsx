import { Routes, Route, Outlet } from 'react-router-dom'
import { AuthProvider, useAuth } from './context/AuthContext'
import { ToastProvider } from './context/ToastContext'
import { Navbar, Toast, LoginRequired } from './components'
import { Environments, Catalog, Run } from './pages'
import { api } from './services/api'
import { useEffect } from 'react'

// Layout component with Navbar
function Layout() {
  return (
    <>
      <Navbar />
      <Outlet />
    </>
  )
}

// Protected route wrapper
function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated } = useAuth()

  if (!isAuthenticated) {
    return (
      <>
        <Navbar />
        <LoginRequired />
      </>
    )
  }

  return <>{children}</>
}

// App wrapper that sets up API unauthorized handler
function AppContent() {
  const { clearToken, login } = useAuth()

  useEffect(() => {
    api.setOnUnauthorized(() => {
      clearToken()
      setTimeout(() => {
        login()
      }, 1500)
    })
  }, [clearToken, login])

  return (
    <Routes>
      <Route
        element={
          <ProtectedRoute>
            <Layout />
          </ProtectedRoute>
        }
      >
        <Route path="/" element={<Environments />} />
        <Route path="/catalog" element={<Catalog />} />
      </Route>
      <Route path="/run/:env" element={<Run />} />
    </Routes>
  )
}

function App() {
  return (
    <AuthProvider>
      <ToastProvider>
        <AppContent />
        <Toast />
      </ToastProvider>
    </AuthProvider>
  )
}

export default App
