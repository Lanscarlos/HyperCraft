import { useState } from 'react'
import type { FormEvent } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import type { Account, InstanceStatus, Role } from '../types'
import { Modal } from './Modal'
import { Select } from './Select'

interface Props {
  /** null when creating. */
  account: Account | null
  roles: Role[]
  instances: InstanceStatus[]
  onCancel: () => void
  onSaved: () => void
}

/**
 * Creating and editing one account.
 *
 * The grant is two controls rather than one list with an "all" checkbox in it,
 * because 全部实例 is not a selection — it covers servers that do not exist
 * yet, which no list of checkboxes can express. Picking the radio and picking
 * nothing are different states and the dialog keeps them apart.
 */
export function AccountDialog({ account, roles, instances, onCancel, onSaved }: Props) {
  const [username, setUsername] = useState(account?.username ?? '')
  const [displayName, setDisplayName] = useState(account?.displayName ?? '')
  const [roleId, setRoleId] = useState(account?.roleId ?? roles.find((r) => !r.builtIn)?.id ?? '')
  const [password, setPassword] = useState('')
  const [disabled, setDisabled] = useState(account?.disabled ?? false)
  const [allInstances, setAllInstances] = useState(account ? account.instances === null : false)
  const [picked, setPicked] = useState<string[]>(account?.instances ?? [])
  const [busy, setBusy] = useState(false)
  const [error, setError] = useState<string | null>(null)

  // An administrator reaches every server by definition, so offering them a
  // grant would be offering a control that does nothing — the panel forces it
  // back to "everything" on write either way.
  const isAdminRole = roles.find((role) => role.id === roleId)?.builtIn ?? false

  const submit = async (event: FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    const grant = allInstances || isAdminRole ? null : picked
    try {
      if (account) {
        await api.updateAccount(account.id, {
          username,
          displayName,
          roleId,
          instances: grant,
          disabled,
        })
        toast(`已保存「${username}」`)
      } else {
        await api.createAccount({ username, displayName, roleId, password, instances: grant })
        toast(`已创建账号「${username}」`)
      }
      onSaved()
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
      setBusy(false)
    }
  }

  return (
    <Modal onClose={onCancel} busy={busy} label={account ? '编辑账号' : '新建账号'}>
      <form className="modal__card modal__card--wide" onSubmit={submit}>
        <h2 className="modal__title">{account ? `编辑「${account.username}」` : '新建账号'}</h2>

        {error && <div className="alert alert--error">{error}</div>}

        <label className="field">
          <span>用户名</span>
          <input
            value={username}
            onChange={(e) => setUsername(e.target.value)}
            autoComplete="off"
            required
            autoFocus={!account}
          />
          <small>登录用。只能是字母、数字、下划线、点和减号。</small>
        </label>

        <label className="field">
          <span>显示名</span>
          <input
            value={displayName}
            onChange={(e) => setDisplayName(e.target.value)}
            autoComplete="off"
            placeholder="可留空"
          />
        </label>

        {!account && (
          <label className="field">
            <span>初始密码</span>
            <input
              type="password"
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              autoComplete="new-password"
              minLength={8}
              required
            />
            <small>至少 8 位。之后可以在列表里重置。</small>
          </label>
        )}

        <div className="field">
          <span>角色</span>
          <Select
            value={roleId}
            onChange={setRoleId}
            className="select--block"
            options={roles.map((role) => ({
              value: role.id,
              label: role.name,
              note: role.builtIn ? '全部能力' : `${role.capabilities.length} 项能力`,
            }))}
          />
        </div>

        <fieldset className="field grant">
          <span>实例授权</span>
          {isAdminRole ? (
            <p className="grant__note">管理员可以管理全部实例，不需要单独授权。</p>
          ) : (
            <>
              <label className="grant__choice">
                <input
                  type="radio"
                  checked={allInstances}
                  onChange={() => setAllInstances(true)}
                />
                <span>
                  全部实例
                  <small>包括以后新建的。</small>
                </span>
              </label>
              <label className="grant__choice">
                <input
                  type="radio"
                  checked={!allInstances}
                  onChange={() => setAllInstances(false)}
                />
                <span>
                  只授权指定实例
                  <small>没勾选任何一个就是一个都碰不到。</small>
                </span>
              </label>

              {!allInstances && (
                <div className="grant__list">
                  {instances.length === 0 ? (
                    <p className="muted">还没有实例可以授权。</p>
                  ) : (
                    instances.map((item) => (
                      <label className="grant__item" key={item.id}>
                        <input
                          type="checkbox"
                          checked={picked.includes(item.id)}
                          onChange={(e) =>
                            setPicked((prev) =>
                              e.target.checked
                                ? [...prev, item.id]
                                : prev.filter((id) => id !== item.id),
                            )
                          }
                        />
                        <span>{item.name}</span>
                      </label>
                    ))
                  )}
                </div>
              )}
            </>
          )}
        </fieldset>

        {account && (
          <label className="field field--check">
            <input
              type="checkbox"
              checked={disabled}
              onChange={(e) => setDisabled(e.target.checked)}
            />
            <span>
              停用这个账号
              <small>保留账号和它的记录，但拒绝一切登录。想彻底清掉用「删除」。</small>
            </span>
          </label>
        )}

        <div className="modal__actions">
          <button type="button" className="btn" onClick={onCancel} disabled={busy}>
            取消
          </button>
          <button type="submit" className="btn btn--primary" disabled={busy} aria-busy={busy}>
            {busy ? '保存中…' : '保存'}
          </button>
        </div>
      </form>
    </Modal>
  )
}
