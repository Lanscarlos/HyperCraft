import { useState } from 'react'

import { api } from '../api'
import type { User } from '../types'

export function Login({ onSignedIn }: { onSignedIn: (user: User) => void }) {
  const [username, setUsername] = useState('admin')
  const [password, setPassword] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    try {
      onSignedIn(await api.login(username, password))
    } catch (err) {
      setError(err instanceof Error ? err.message : '登录失败')
    } finally {
      setBusy(false)
    }
  }

  return (
    <div className="login">
      <form className="login__card" onSubmit={submit}>
        <h1 className="login__title">HyperCraft</h1>
        <p className="login__subtitle">Minecraft 服务器面板</p>

        <label className="field">
          <span>用户名</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="username"
            autoFocus
          />
        </label>

        <label className="field">
          <span>密码</span>
          <input
            type="password"
            value={password}
            onChange={(e) => setPassword(e.target.value)}
            autoComplete="current-password"
          />
        </label>

        {error && <div className="alert alert--error">{error}</div>}

        <button className="btn btn--primary" type="submit" disabled={busy}>
          {busy ? '登录中…' : '登录'}
        </button>

        <p className="login__hint">
          首次启动时，初始密码会打印在面板的启动日志里。忘记密码可运行{' '}
          <code>hypercraft -reset-password</code>。
        </p>
      </form>
    </div>
  )
}
