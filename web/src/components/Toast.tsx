import { useToast } from '../context/ToastContext'

export function Toast() {
  const { toast } = useToast()

  if (!toast) return null

  return (
    <div className={`toast ${toast.type} show`}>
      {toast.message}
    </div>
  )
}
