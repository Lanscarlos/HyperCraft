import { useCallback, useEffect, useState } from 'react'

import { api } from '../api'
import type { AuthEvent, AuthEventKind, User } from '../types'
import { Page } from './Page'

/**
 * Who has been touching the panel's credentials, and from where.
 *
 * The panel already writes every one of these to its log, but that log goes to
 * stdout and from there to journald — so the two questions an operator asks
 * after putting a reverse proxy or an accelerator in front ("which address is
 * actually reaching the panel?" and "has anyone else signed in?") could only be
 * answered over SSH. This is the same data, in the panel.
 *
 * It is deliberately not a full audit log: the list lives in memory and a
 * restart clears it. Anything that has to survive being restarted belongs in
 * journald, which still has it.
 */
export function SecurityPage() {
  const [events, setEvents] = useState<AuthEvent[] | null>(null)
  const [me, setMe] = useState<User | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [refreshing, setRefreshing] = useState(false)

  const load = useCallback(async () => {
    setRefreshing(true)
    try {
      // Both in one go: the current connection is only interesting next to the
      // addresses in the list, and vice versa.
      const [who, list] = await Promise.all([api.me(), api.listAuthEvents()])
      setMe(who)
      setEvents(list)
      setError(null)
    } catch (err) {
      setError(err instanceof Error ? err.message : '读取失败')
    } finally {
      setRefreshing(false)
    }
  }, [])

  useEffect(() => {
    void load()
  }, [load])

  const proxied = me !== null && me.client !== me.remote

  return (
    <Page
      title="登录记录"
      lead="面板收到的地址，以及最近发生过的登录、配对和限流。这份列表只存在内存里，面板一重启（包括自动更新）就清空 —— 需要长期留存的记录在系统日志里，用 journalctl -u hypercraft 看。"
    >
      {error && <div className="alert alert--error">{error}</div>}

      <section className="panel">
        <h3 className="panel__title">当前连接</h3>

        {me === null ? (
          <p className="muted">正在读取…</p>
        ) : (
          <>
            <dl className="update__meta">
              <div>
                <dt>客户端地址</dt>
                <dd>{me.client}</dd>
              </div>
              <div>
                <dt>TCP 对端</dt>
                <dd>{me.remote}</dd>
              </div>
            </dl>
            <small className="update__note">
              {proxied ? (
                <>
                  两者不同，说明面板正透过可信代理看你：<strong>{me.remote}</strong>{' '}
                  是代理回源到面板的地址，<strong>{me.client}</strong> 是它转告面板的真实客户端。
                  防火墙白名单要放行的是前者。
                </>
              ) : (
                <>
                  两者相同，说明面板是直接看到你的 —— 要么没有代理，要么代理的地址还没写进{' '}
                  <code>panel.json</code> 的 <code>trustedProxies</code>，此时面板不会相信{' '}
                  <code>X-Forwarded-For</code>（那个头谁都能伪造）。
                </>
              )}
            </small>
            {!proxied && (
              <small className="update__note">
                用加速器或 CDN 时请注意：这里只显示<strong>这一次</strong>连接的地址，
                而回源节点通常有一批。配防火墙白名单请以服务商控制台给出的完整清单为准，
                这一页适合用来验证清单对不对。
              </small>
            )}
          </>
        )}
      </section>

      <section className="panel">
        <div className="chart-head">
          <h3 className="panel__title">最近事件</h3>
          <button className="link" onClick={() => void load()} disabled={refreshing}>
            {refreshing ? '刷新中…' : '刷新'}
          </button>
        </div>

        {events === null ? (
          <p className="muted">正在读取…</p>
        ) : events.length === 0 ? (
          <p className="muted">面板启动后还没有发生过登录相关的事件。</p>
        ) : (
          <div className="table-scroll">
            <table className="data-table auth-events">
              <thead>
                <tr>
                  <th>时间</th>
                  <th>事件</th>
                  <th>用户名</th>
                  <th>客户端地址</th>
                  {proxied && <th>TCP 对端</th>}
                </tr>
              </thead>
              <tbody>
                {events.map((event, index) => (
                  <tr key={`${event.at}-${index}`}>
                    <td className="muted">{formatTime(event.at)}</td>
                    <td>
                      <span className={`badge ${KIND_BADGE[event.kind] ?? ''}`}>
                        {KIND_LABELS[event.kind] ?? event.kind}
                      </span>
                      {event.count > 1 && <span className="muted"> ×{event.count}</span>}
                      {event.detail && <span className="muted"> {event.detail}</span>}
                    </td>
                    <td>{event.username || <span className="muted">—</span>}</td>
                    <td>{event.client}</td>
                    {proxied && <td className="muted">{event.remote}</td>}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}

        <small className="update__note">
          失败的登录里显示的用户名是对方<strong>输入</strong>的，不代表这个账号存在。
          连续重复的同类事件会折叠成一行并标出次数 —— 否则一次限流就能把其他记录挤出这份列表。
        </small>
      </section>
    </Page>
  )
}

const KIND_LABELS: Record<AuthEventKind, string> = {
  signin: '登录成功',
  'signin-failed': '密码错误',
  throttled: '已限流',
  paired: '配对设备',
  'pair-failed': '配对失败',
  unpaired: '解除配对',
  'password-changed': '修改密码',
  'token-rejected': '设备令牌无效',
}

const KIND_BADGE: Record<AuthEventKind, string> = {
  signin: 'badge--ok',
  'signin-failed': 'badge--danger',
  throttled: 'badge--warn',
  paired: 'badge--ok',
  'pair-failed': 'badge--danger',
  unpaired: '',
  'password-changed': 'badge--warn',
  'token-rejected': 'badge--warn',
}

/**
 * Minute precision with the date only when it is not today: these events are
 * read to answer "what just happened", and a full timestamp on every row makes
 * the column wide enough to push the addresses off a phone screen.
 */
function formatTime(iso: string): string {
  const at = new Date(iso)
  const sameDay = at.toDateString() === new Date().toDateString()
  return at.toLocaleString('zh-CN', {
    month: sameDay ? undefined : '2-digit',
    day: sameDay ? undefined : '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
    hour12: false,
  })
}
