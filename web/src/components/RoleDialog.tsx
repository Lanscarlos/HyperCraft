import { useMemo, useState } from 'react'
import type { FormEvent } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import type { Capability, CapabilityInfo, Role } from '../types'
import { Button } from './Button'
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
  // One path per line, which is how people write lists of paths. Parsed on
  // save; the panel cleans them further (see users.cleanPaths).
  const [paths, setPaths] = useState((role?.paths ?? []).join('\n'))
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

  // Which of the held capabilities make a folder rule pointless. Derived from
  // the panel's own dangerous flag rather than a list in here, so a capability
  // added later is covered without a front-end change. 编辑、上传与删除文件 is
  // excluded: it is the capability being confined, not one that escapes.
  const defeating = useMemo(
    () =>
      capabilities
        .filter((cap) => cap.dangerous && cap.id !== 'instance:files:write' && held.has(cap.id))
        .map((cap) => cap.title),
    [capabilities, held],
  )

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
    const folders = paths
      .split('\n')
      .map((line) => line.trim())
      .filter((line) => line !== '')
    try {
      if (role) {
        await api.updateRole(role.id, name, picked, folders)
        toast(`已保存角色「${name}」`)
      } else {
        await api.createRole(name, picked, folders)
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

        {!readOnly && (
          <label className="field">
            <span>目录限制</span>
            <textarea
              value={paths}
              onChange={(e) => setPaths(e.target.value)}
              rows={3}
              spellCheck={false}
              placeholder={'plugins/MyPlugin\nplugins/另一个插件'}
            />
            <small>
              一行一个，相对实例目录。留空表示整个实例目录。
              <strong>只管文件管理器</strong> —— 编辑服务器配置、导入建筑、换核心各有各的能力，不受这里限制。
            </small>
          </label>
        )}

        {/* The one thing an operator cannot be expected to work out: a folder
            rule is worth nothing next to a capability that runs code. Said
            where the rule is typed, naming the capabilities that defeat it. */}
        {!readOnly && defeating.length > 0 && paths.trim() !== '' && (
          <div className="alert alert--warn">
            目录限制对这个角色没有实际意义：它同时持有 <strong>{defeating.join('、')}</strong> ——
            能决定服务端跑什么代码，就能绕开任何目录规则。要让限制真正生效，先取消这些能力。
          </div>
        )}

        <div className="modal__actions">
          <Button type="button" onClick={onCancel} disabled={busy}>
            {readOnly ? '关闭' : '取消'}
          </Button>
          {!readOnly && (
            <Button type="submit" variant="primary" disabled={busy} aria-busy={busy}>
              {busy ? '保存中…' : '保存'}
            </Button>
          )}
        </div>
      </form>
    </Modal>
  )
}
