import { useState } from 'react'

import { api } from '../api'

interface Props {
  onChanged: () => void
  onCancel: () => void
}

export function ChangePasswordDialog({ onChanged, onCancel }: Props) {
  const [current, setCurrent] = useState('')
  const [next, setNext] = useState('')
  const [confirm, setConfirm] = useState('')
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)

  const submit = async (event: React.FormEvent) => {
    event.preventDefault()
    if (next !== confirm) {
      setError('两次输入的新密码不一致')
      return
    }
    setBusy(true)
    setError(null)
    try {
      await api.changePassword(current, next)
      onChanged()
    } catch (err) {
      setError(err instanceof Error ? err.message : '修改失败')
      setBusy(false)
    }
  }

  return (
    <div className="modal" role="dialog" aria-modal="true">
      <form className="modal__card" onSubmit={submit}>
        <h2 className="modal__title">修改密码</h2>
        <p className="modal__lead">修改后所有登录状态都会失效，需要重新登录。</p>

        <label className="field">
          <span>当前密码</span>
          <input
            type="password"
            value={current}
            onChange={(e) => setCurrent(e.target.value)}
            autoComplete="current-password"
            required
            autoFocus
          />
        </label>

        <label className="field">
          <span>新密码</span>
          <input
            type="password"
            value={next}
            onChange={(e) => setNext(e.target.value)}
            autoComplete="new-password"
            minLength={8}
            required
          />
          <small>至少 8 位。</small>
        </label>

        <label className="field">
          <span>确认新密码</span>
          <input
            type="password"
            value={confirm}
            onChange={(e) => setConfirm(e.target.value)}
            autoComplete="new-password"
            required
          />
        </label>

        {error && <div className="alert alert--error">{error}</div>}

        <div className="modal__actions">
          <button className="btn" type="button" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button className="btn btn--primary" type="submit" disabled={busy}>
            {busy ? '提交中…' : '修改密码'}
          </button>
        </div>
      </form>
    </div>
  )
}
