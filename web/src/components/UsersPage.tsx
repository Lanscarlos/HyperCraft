import { useCallback, useEffect, useMemo, useState } from 'react'

import { api } from '../api'
import { ask } from '../confirm'
import { toast } from '../toast'
import type {
  Account,
  Capability,
  CapabilityInfo,
  InstanceStatus,
  Role,
} from '../types'
import { AccountDialog } from './AccountDialog'
import { Page } from './Page'
import { RoleDialog } from './RoleDialog'

interface Props {
  /** The servers this administrator can see, for the grant picker. */
  instances: InstanceStatus[]
  /** The signed-in account's username, so its own row can say so. */
  me: string
}

/**
 * Who can get into this panel, and what they may do once they are in.
 *
 * Two lists rather than one page per thing: an account is only meaningful
 * through its role, and a role is only meaningful through the accounts holding
 * it, so editing either while unable to see the other is how you end up with
 * six roles that differ by one capability nobody can name.
 *
 * What this page cannot do, and says so out loud: make 开发 safe. Deciding what
 * code a server runs is the same as running code on this machine — see
 * docs/security.md. The role editor marks those capabilities rather than
 * pretending the distinction is finer than it is.
 */
export function UsersPage({ instances, me }: Props) {
  const [accounts, setAccounts] = useState<Account[] | null>(null)
  const [roles, setRoles] = useState<Role[] | null>(null)
  const [caps, setCaps] = useState<CapabilityInfo[] | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState<string | null>(null)

  const [editing, setEditing] = useState<Account | 'new' | null>(null)
  const [editingRole, setEditingRole] = useState<Role | 'new' | null>(null)

  const load = useCallback(async () => {
    try {
      const [nextAccounts, nextRoles, nextCaps] = await Promise.all([
        api.listAccounts(),
        api.listRoles(),
        api.listCapabilities(),
      ])
      setAccounts(nextAccounts)
      setRoles(nextRoles)
      setCaps(nextCaps)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取失败')
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  // Both lists come back from every write, so one handler covers all of them
  // and the page can never show a stale row next to a fresh one.
  const run = async (id: string, work: () => Promise<unknown>) => {
    setBusy(id)
    setError(null)
    try {
      await work()
      await load()
      return true
    } catch (err) {
      setError(err instanceof Error ? err.message : '操作失败')
      return false
    } finally {
      setBusy(null)
    }
  }

  const resetPassword = async (account: Account) => {
    const next = window.prompt(`给「${account.username}」设置新密码（至少 8 位）`)
    if (!next) return
    const ok = await run(account.id, () => api.setAccountPassword(account.id, next))
    if (ok) toast(`已重置「${account.username}」的密码，该账号的会话和设备全部失效`)
  }

  const signOut = async (account: Account) => {
    const ok = await ask({
      title: `强制「${account.username}」下线？`,
      lead: '该账号所有浏览器会话和配对设备会立刻失效。',
      detail: '密码不变，本人重新登录即可；配对过的 App 需要重新配对。',
      confirmLabel: '强制下线',
      danger: true,
    })
    if (!ok) return
    if (await run(account.id, () => api.signOutAccount(account.id))) {
      toast(`已让「${account.username}」下线`)
    }
  }

  const remove = async (account: Account) => {
    const ok = await ask({
      title: `删除账号「${account.username}」？`,
      lead: '该账号会立刻失去访问，配对设备一并解除。',
      detail: '这一步不可撤销。只是想暂时停掉的话，用「编辑」里的停用开关。',
      confirmLabel: '删除账号',
      danger: true,
    })
    if (!ok) return
    if (await run(account.id, () => api.deleteAccount(account.id))) {
      toast(`已删除「${account.username}」`)
    }
  }

  const removeRole = async (role: Role) => {
    const ok = await ask({
      title: `删除角色「${role.name}」？`,
      lead: '角色删掉之后不影响已有账号的登录，但没有账号能再被指派到它。',
      confirmLabel: '删除角色',
      danger: true,
    })
    if (!ok) return
    if (await run(role.id, () => api.deleteRole(role.id))) toast(`已删除角色「${role.name}」`)
  }

  const names = useMemo(
    () => new Map(instances.map((item) => [item.id, item.name])),
    [instances],
  )

  return (
    <Page
      title="账号与角色"
      lead="一个账号持有一个角色，角色是一组能力的集合；实例授权是另一个维度，说的是这个账号能碰哪几个服。两样都满足才放行。"
    >
      {error && <div className="alert alert--error">{error}</div>}

      <section className="panel">
        <div className="panel__head">
          <h3 className="panel__title">账号</h3>
          <button className="btn btn--primary btn--row" onClick={() => setEditing('new')}>
            新建账号
          </button>
        </div>

        {accounts === null ? (
          <p className="muted">正在读取…</p>
        ) : (
          <div className="acct-list">
            {accounts.map((account) => (
              <div className="acct-row" key={account.id}>
                <div className="acct-row__main">
                  <strong>{account.username}</strong>
                  {account.displayName && (
                    <span className="acct-row__alias">{account.displayName}</span>
                  )}
                  <span className="badge">{account.roleName}</span>
                  {account.disabled && <span className="badge badge--warn">已停用</span>}
                  {account.username === me && <span className="badge badge--muted">这是你</span>}
                  <span className="acct-row__spacer" />
                  <span className="acct-row__actions">
                    <button
                      className="link"
                      onClick={() => setEditing(account)}
                      disabled={busy !== null}
                    >
                      编辑
                    </button>
                    <button
                      className="link"
                      onClick={() => void resetPassword(account)}
                      disabled={busy !== null}
                    >
                      重置密码
                    </button>
                    <button
                      className="link"
                      onClick={() => void signOut(account)}
                      disabled={busy !== null}
                    >
                      强制下线
                    </button>
                    <button
                      className="link link--danger"
                      onClick={() => void remove(account)}
                      disabled={busy !== null}
                    >
                      删除
                    </button>
                  </span>
                </div>
                <div className="acct-row__meta">
                  {describeGrant(account.instances, names)}
                  {' · '}
                  {account.devices > 0 ? `${account.devices} 台配对设备` : '没有配对设备'}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      <section className="panel">
        <div className="panel__head">
          <h3 className="panel__title">角色</h3>
          <button className="btn btn--row" onClick={() => setEditingRole('new')}>
            新建角色
          </button>
        </div>
        <p className="panel__lead">
          管理员是内置角色，恒等于全部能力 —— 以后版本新增一条能力，管理员自动就有。它改不了也删不掉。
        </p>

        {roles === null ? (
          <p className="muted">正在读取…</p>
        ) : (
          <div className="acct-list">
            {roles.map((role) => (
              <div className="acct-row" key={role.id}>
                <div className="acct-row__main">
                  <strong>{role.name}</strong>
                  {role.builtIn && <span className="badge badge--muted">内置</span>}
                  <span className="acct-row__spacer" />
                  <span className="acct-row__actions">
                    <button
                      className="link"
                      onClick={() => setEditingRole(role)}
                      disabled={busy !== null}
                    >
                      {role.builtIn ? '查看' : '编辑'}
                    </button>
                    {!role.builtIn && (
                      <button
                        className="link link--danger"
                        onClick={() => void removeRole(role)}
                        disabled={busy !== null || role.users > 0}
                        title={role.users > 0 ? '还有账号在用这个角色，先把它们改成别的角色' : undefined}
                      >
                        删除
                      </button>
                    )}
                  </span>
                </div>
                <div className="acct-row__meta">
                  {role.builtIn ? '全部能力' : `${role.capabilities.length} 项能力`}
                  {' · '}
                  {role.users > 0 ? `${role.users} 个账号在用` : '没有账号在用'}
                  {role.paths.length > 0 && (
                    <>
                      {' · '}
                      文件限于 {role.paths.join('、')}
                    </>
                  )}
                  {dangerCount(role, caps) > 0 && (
                    <>
                      {' · '}
                      <span className="acct-row__danger">
                        含 {dangerCount(role, caps)} 项高危能力
                      </span>
                    </>
                  )}
                </div>
              </div>
            ))}
          </div>
        )}
      </section>

      {editing && roles && (
        <AccountDialog
          account={editing === 'new' ? null : editing}
          roles={roles}
          instances={instances}
          onCancel={() => setEditing(null)}
          onSaved={() => {
            setEditing(null)
            void load()
          }}
        />
      )}

      {editingRole && caps && (
        <RoleDialog
          role={editingRole === 'new' ? null : editingRole}
          capabilities={caps}
          onCancel={() => setEditingRole(null)}
          onSaved={() => {
            setEditingRole(null)
            void load()
          }}
        />
      )}
    </Page>
  )
}

/**
 * A grant as a sentence. `null` is every server — including ones that do not
 * exist yet — and that is a different statement from a list that happens to
 * name all of today's, so the two never read the same.
 */
function describeGrant(grant: string[] | null, names: Map<string, string>): string {
  if (grant === null) return '可以管理全部实例'
  if (grant.length === 0) return '没有授权任何实例'
  const listed = grant.map((id) => names.get(id) ?? id)
  if (listed.length <= 3) return `可以管理 ${listed.join('、')}`
  return `可以管理 ${listed.slice(0, 3).join('、')} 等 ${listed.length} 个实例`
}

function dangerCount(role: Role, caps: CapabilityInfo[] | null): number {
  if (!caps || role.builtIn) return 0
  const dangerous = new Set(caps.filter((cap) => cap.dangerous).map((cap) => cap.id))
  return role.capabilities.filter((cap: Capability) => dangerous.has(cap)).length
}
