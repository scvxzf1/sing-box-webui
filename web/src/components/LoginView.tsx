import { KeyRound, LoaderCircle, ShieldCheck } from 'lucide-react'
import { useState, type FormEvent } from 'react'
import { ApiError, login } from '../api/client'

interface LoginViewProps {
  checking?: boolean
  onAuthenticated: () => void
}

export function LoginView({ checking = false, onAuthenticated }: LoginViewProps) {
  const [token, setToken] = useState('')
  const [submitting, setSubmitting] = useState(false)
  const [error, setError] = useState('')

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (!token.trim() || submitting) return
    setSubmitting(true)
    setError('')
    try {
      await login(token.trim())
      onAuthenticated()
    } catch (reason) {
      setError(reason instanceof ApiError && reason.code === 'token_invalid' ? '访问令牌不正确' : '无法连接到本机服务')
    } finally {
      setSubmitting(false)
    }
  }

  return (
    <main className="login-shell">
      <section className="login-panel" aria-labelledby="login-title">
        <div className="login-brand">
          <span className="brand-mark" aria-hidden="true"><img src="/brand-mark.svg" alt="" /></span>
          <span>sing-box WebUI</span>
        </div>
        <div className="login-heading">
          <ShieldCheck size={22} aria-hidden="true" />
          <div><h1 id="login-title">访问验证</h1><p>输入项目配置文件中的 Web Token</p></div>
        </div>
        {checking ? (
          <div className="login-checking"><LoaderCircle size={18} className="spin" aria-hidden="true" />正在验证会话</div>
        ) : (
          <form onSubmit={submit} className="login-form">
            <label htmlFor="web-token">Web Token</label>
            <div className="login-token-field"><KeyRound size={17} aria-hidden="true" /><input id="web-token" type="password" autoComplete="current-password" autoFocus value={token} onChange={(event) => setToken(event.target.value)} /></div>
            {error && <p className="login-error" role="alert">{error}</p>}
            <button className="button button--primary" type="submit" disabled={!token.trim() || submitting}>{submitting ? <LoaderCircle size={16} className="spin" aria-hidden="true" /> : <ShieldCheck size={16} aria-hidden="true" />}验证并进入</button>
          </form>
        )}
      </section>
    </main>
  )
}
