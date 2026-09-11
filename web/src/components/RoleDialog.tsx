import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import type { Capability, CapabilityInfo, Role } from '../types'
import { Modal } from './Modal'

interface Props {
  /** null when creating; a built-in role is shown read-only. */
  role: Role | null
  capabilities: CapabilityInfo[]
  onCancel: () => void
  onSaved: () => void
}

/**
 * The capability picker.
 *
 * The list comes from the panel rather than from a copy in here, so a
 * capability added in a later release appears without a front-end change — and
 * so the wording next to each one is the wording the people who wrote the check
 * chose.
 *
 * The dangerous ones are separated rather than marked in place. Mixing them in
 * with a small icon makes granting one an accident; putting them below a rule
 * that says what they actually mean makes it a decision. They are not weaker
 * forms of administrator — see docs/security.md.
 */
export function RoleDialog({ role, capabilities, onCancel, onSaved }: Props) {
  const readOnly = role?.builtIn ?? false
  const [name, setName] = useState(role?.name ?? '')
  const [held, setHeld] = useState<Set<Capability>>(new Set(role?.capabilities ?? []))
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  const groups = useMemo(() => {
    const instance = capabilities.filter((cap) => cap.scope === 'instance' && !cap.dangerous)
    const panel = capabilities.filter((cap) => cap.scope === 'panel' && !cap.dangerous)
    const dangerous = capabilities.filter((cap) => cap.dangerous)
    return [
      { key: 'instance', label: '实例', note: '受实例授权限制', rows: instance },
      { key: 'panel', label: '面板', note: '对整个面板生效', rows: panel },
      { key: 'danger', label: '高危', note: '', rows: dangerous },
    ]
  }, [capabilities])

  const toggle = (id: Capability, on: boolean) => {
    setHeld((prev) => {
      const next = new Set(prev)
      if (on) next.add(id)
      else next.delete(id)
      return next
    })
  }

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    if (readOnly) {
      onCancel()
      return
    }
    setBusy(true)
    setError(null)
    const picked = capabilities.filter((cap) => held.has(cap.id)).map((cap) => cap.id)
    try {
      if (role) {
        await api.updateRole(role.id, name, picked)
        toast(`已保存角色「${name}」`)
      } else {
        await api.createRole(name, picked)
        toast(`已创建角色「${name}」`)
      }
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onCancel} busy={busy} label={role ? '编辑角色' : '新建角色'}>
      <form className="modal__card modal__card--wide" onSubmit={submit}>
        <h2 className="modal__title">
          {readOnly ? `角色「${role?.name}」` : role ? `编辑「${role.name}」` : '新建角色'}
        </h2>
        {readOnly && (
          <p className="modal__lead">
            内置角色，恒等于全部能力，改不了也删不掉 —— 以后版本新增一条能力，它自动就有。
          </p>
        )}

        {error && <div className="alert alert--error">{error}</div>}

        <label className="field">
          <span>角色名</span>
          <input
            value={readOnly ? (role?.name ?? '') : name}
            onChange={(e) => setName(e.target.value)}
            disabled={readOnly}
            required
            autoFocus={!role}
          />
        </label>

        <div className="cap-picker">
          {groups.map((group) => (
            <fieldset
              className={`cap-group${group.key === 'danger' ? ' cap-group--danger' : ''}`}
              key={group.key}
            >
              <legend>
                {group.label}
                {group.note && <small>{group.note}</small>}
              </legend>
              {group.key === 'danger' && (
                <p className="cap-group__warn">
                  这些能力等同于把面板所在的系统账号交出去：能决定服务端跑什么代码，就能读到面板的
                  密码哈希、数据库明文密码和别人的实例目录。给出去之前先确认这个人本来就该能登进这台
                  机器。
                </p>
              )}
              <div className="cap-group__rows">
                {group.rows.map((cap) => (
                  <label className="cap-option" key={cap.id}>
                    <input
                      type="checkbox"
                      checked={readOnly || held.has(cap.id)}
                      disabled={readOnly}
                      onChange={(e) => toggle(cap.id, e.target.checked)}
                    />
                    <span>
                      {cap.title}
                      {cap.note && <small>{cap.note}</small>}
                    </span>
                  </label>
                ))}
              </div>
            </fieldset>
          ))}
        </div>

        <div className="modal__actions">
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            {readOnly ? '关闭' : '取消'}
          </button>
          {!readOnly && (
            <button type="submit" className="btn btn--primary" disabled={busy} aria-busy={busy}>
              {busy ? '保存中…' : '保存'}
            </button>
          )}
        </div>
      </form>
    </Modal>
  )
}
