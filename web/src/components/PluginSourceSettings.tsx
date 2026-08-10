import { useState } from 'react'

import type { PluginTokenInfo } from '../types'
import type { PluginController } from '../usePlugins'
import { Page } from './Page'

/**
 * The credentials private repositories are read with, and the proxy their jars
 * come through.
 *
 * This was a page called 插件源 and it held two things that are not the same
 * kind of thing. Registering a repository is an *action*, done whenever a new
 * plugin turns up, and it now sits with the other three ways a plugin enters
 * the library — the + 添加插件 menu on 我的库, where it also gets to show what
 * it found before you agree to it. The tokens and the download mirror are
 * *settings*: set on the day the panel is installed or the day something stops
 * working, and never again. Only the second half is a page, and this is it.
 */
export function PluginSourceSettings({ plugins }: { plugins: PluginController }) {
  const { library, busy } = plugins

  return (
    <Page
      title="GitHub 集成"
      lead="三个插件站覆盖了绝大多数插件，但覆盖不了只发在作者自己 GitHub Release 上的那种 —— 包括你自己那个私有仓库。这一页管两件事：读 GitHub 用的访问令牌，以及 jar 走哪个下载源。想加一个仓库当插件源，在「资源库 → 插件库」的「+ 添加插件」里。"
    >
      <GitHubTokenPanel
        tokens={library?.tokens ?? []}
        busy={busy}
        onAdd={plugins.addToken}
        onUpdate={plugins.updateToken}
        onRemove={plugins.removeToken}
      />

      {/* Only once the library has answered. The picker decides on mount
          whether the stored value is one of the offered mirrors or a custom
          prefix, and mounting it against an empty list makes every panel come
          up on 自定义 with the mirror's id typed into the box. */}
      {library && (
        <MirrorPanel
          mirrors={library.mirrors}
          current={library.mirror}
          busy={busy}
          onChange={(mirror) => plugins.setMirror(mirror)}
        />
      )}

      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
    </Page>
  )
}

/**
 * The credentials private repositories are read with.
 *
 * A list rather than one box, because "the operator's GitHub account" is not
 * one thing. A fine-grained token is minted by one account and scoped to a set
 * of that account's repositories: the token that reads the plugin in your own
 * namespace cannot be granted access to the org's private fork. With a single
 * credential the only way to cover both is a classic token with blanket
 * <code>repo</code> scope — the broadest key GitHub will mint, held by a panel
 * that needs to read two repositories. So each plugin names the token it is
 * read with, and this page is where the tokens are.
 *
 * Write-only on purpose: the panel will say a token is there and show its last
 * four characters, and that is all — a token that can be read back out of a
 * page is a token that leaks with the page. Fixing a wrong one is done by
 * pasting a new one over it, which keeps the id and so keeps the plugins
 * pointing at it.
 */
function GitHubTokenPanel({
  tokens,
  busy,
  onAdd,
  onUpdate,
  onRemove,
}: {
  tokens: PluginTokenInfo[]
  busy: boolean
  onAdd: (name: string, token: string) => Promise<boolean>
  onUpdate: (
    id: string,
    input: { name?: string; token?: string; default?: boolean },
  ) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  const [name, setName] = useState('')
  const [token, setToken] = useState('')

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!token.trim()) return
    if (await onAdd(name.trim(), token.trim())) {
      setName('')
      setToken('')
    }
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">GitHub 访问令牌</h2>
        {tokens.length > 0 && <span className="badge badge--ok">{tokens.length} 个</span>}
      </div>
      <p className="chart-note">
        自己写的插件发在私有仓库里时，面板得先能证明「我是你」才看得见它。可以存好几个 ——
        自己号一个、公司 org 一个 —— 添加插件时挑用哪个，不用为了让一把钥匙开两把锁去开
        classic 令牌的全量 <code>repo</code> 权限。没指定的插件走「默认」那个。
        顺带一提，就算全是公开仓库，配了令牌也值：匿名调用每小时只有 60 次，
        「检查全部更新」一次就是一个插件一次调用，插件一多就不够用。
      </p>

      {tokens.map((entry) => (
        <TokenRow
          key={entry.id}
          token={entry}
          busy={busy}
          onUpdate={onUpdate}
          onRemove={onRemove}
        />
      ))}

      <form onSubmit={(event) => void save(event)}>
        <div className="field-row">
          <label className="field">
            <span>名字</span>
            <input
              value={name}
              placeholder={`令牌 ${tokens.length + 1}`}
              onChange={(e) => setName(e.target.value)}
            />
            <small>给自己认的，比如「我的私库」「公司 org」。</small>
          </label>
          <label className="field">
            <span>令牌</span>
            <input
              type="password"
              value={token}
              autoComplete="off"
              spellCheck={false}
              placeholder="github_pat_… 或 ghp_…"
              onChange={(e) => setToken(e.target.value)}
            />
            <small>
              在 GitHub 的 Settings → Developer settings → Personal access tokens 里生成。
              fine-grained 令牌只要给目标仓库的 <code>Contents: Read-only</code> 权限；
              classic 令牌勾 <code>repo</code>。存在面板自己的 panel.json 里（0600），
              只发给 api.github.com，不会经过下载源。
            </small>
          </label>
        </div>
        <div className="field__tools">
          <button className="btn btn--primary" type="submit" disabled={busy || !token.trim()}>
            添加令牌
          </button>
        </div>
      </form>
    </section>
  )
}

/**
 * One stored credential: what it is called, what it is worth, and the three
 * things that can be done to it without detaching the plugins that name it.
 *
 * Deleting is the one with a consequence worth spelling out, which is why the
 * count of plugins reading through it is on the row rather than somewhere else.
 */
function TokenRow({
  token,
  busy,
  onUpdate,
  onRemove,
}: {
  token: PluginTokenInfo
  busy: boolean
  onUpdate: (
    id: string,
    input: { name?: string; token?: string; default?: boolean },
  ) => Promise<boolean>
  onRemove: (id: string) => Promise<boolean>
}) {
  const [replacing, setReplacing] = useState(false)
  const [secret, setSecret] = useState('')
  const [name, setName] = useState(token.name)

  const budget = token.budget
  const low = budget && budget.limit > 0 && budget.remaining <= budget.limit * 0.2

  const rename = async () => {
    const trimmed = name.trim()
    if (trimmed === '' || trimmed === token.name) {
      setName(token.name)
      return
    }
    if (!(await onUpdate(token.id, { name: trimmed }))) setName(token.name)
  }

  const replace = async () => {
    if (!secret.trim()) return
    if (await onUpdate(token.id, { token: secret.trim() })) {
      setSecret('')
      setReplacing(false)
    }
  }

  return (
    <div className="tokenrow">
      <div className="tokenrow__head">
        <input
          className="tokenrow__name"
          value={name}
          disabled={busy}
          aria-label="令牌名字"
          onChange={(event) => setName(event.target.value)}
          onBlur={() => void rename()}
        />
        {token.hint && <span className="muted">···{token.hint}</span>}
        {token.default ? (
          <span className="badge badge--ok">默认</span>
        ) : (
          <button
            className="link"
            type="button"
            disabled={busy}
            title="没有指定令牌的插件会用这一个"
            onClick={() => void onUpdate(token.id, { default: true })}
          >
            设为默认
          </button>
        )}
        {/* The number the argument for a token actually rests on, rather than
            the argument. "60 次一小时" is abstract until it is 7 左右. */}
        {budget && budget.limit > 0 && (
          <span className={`badge${low ? ' badge--warn' : ''}`}>
            API 余额 {budget.remaining} / {budget.limit}
          </span>
        )}
        <span className="tokenrow__used muted">
          {token.usedBy > 0 ? `${token.usedBy} 个插件在用` : '暂时没有插件在用'}
        </span>
        <button className="btn btn--small" type="button" disabled={busy} onClick={() => setReplacing(!replacing)}>
          换令牌
        </button>
        <button
          className="btn btn--small"
          type="button"
          disabled={busy}
          onClick={() => {
            const warning =
              token.usedBy > 0
                ? `有 ${token.usedBy} 个插件在用这个令牌，删掉之后它们会读不到仓库，得改成别的令牌。确定吗？`
                : '删掉这个令牌？'
            if (window.confirm(warning)) void onRemove(token.id)
          }}
        >
          删除
        </button>
      </div>

      {replacing && (
        <div className="update__mirror-custom">
          <input
            type="password"
            value={secret}
            autoComplete="off"
            spellCheck={false}
            placeholder="粘贴新令牌，旧的会被顶掉"
            aria-label="新的访问令牌"
            onChange={(event) => setSecret(event.target.value)}
          />
          <button
            className="btn"
            type="button"
            disabled={busy || !secret.trim()}
            onClick={() => void replace()}
          >
            保存
          </button>
        </div>
      )}
    </div>
  )
}

/**
 * Which proxy plugin jars come through.
 *
 * Separate from the panel's own update mirror even though both proxy the same
 * CDN: the panel updates a few times a year and plugins download weekly, so the
 * proxy that works for one is worth choosing without digging through a page
 * about the other.
 */
function MirrorPanel({
  mirrors,
  current,
  busy,
  onChange,
}: {
  mirrors: { id: string; name: string; note: string; prefix?: string }[]
  current: string
  busy: boolean
  onChange: (mirror: string) => Promise<boolean>
}) {
  const known = mirrors.some((mirror) => mirror.id === current)
  const [custom, setCustom] = useState(known ? '' : current)
  const [editing, setEditing] = useState(!known)

  const selection = editing ? 'custom' : current

  return (
    <section className="panel">
      <h2 className="panel__title">下载源</h2>
      <p className="chart-note">
        只影响 jar 的下载速度：版本列表、更新检查始终直连 api.github.com（这些代理不代理它），
        私有仓库的 jar 也只走认证过的 API，不会经过任何第三方。
      </p>

      {mirrors.map((mirror) => (
        <label className="checkbox" key={mirror.id}>
          <input
            type="radio"
            name="plugin-mirror"
            checked={selection === mirror.id}
            disabled={busy}
            onChange={() => {
              setEditing(false)
              void onChange(mirror.id)
            }}
          />
          <span>
            {mirror.name}
            <small style={{ display: 'block' }}>
              {mirror.note}
              {mirror.prefix && ` · ${mirror.prefix}`}
            </small>
          </span>
        </label>
      ))}

      <label className="checkbox">
        <input
          type="radio"
          name="plugin-mirror"
          checked={selection === 'custom'}
          disabled={busy}
          onChange={() => setEditing(true)}
        />
        <span>
          自定义
          <small style={{ display: 'block' }}>自己搭的代理，填前缀，GitHub 链接会拼在它后面</small>
        </span>
      </label>

      {editing && (
        <div className="update__mirror-custom">
          <input
            value={custom}
            onChange={(e) => setCustom(e.target.value)}
            placeholder="https://example.com/"
            aria-label="自定义下载源"
          />
          <button
            className="btn"
            type="button"
            disabled={busy || !custom.trim()}
            onClick={() => void onChange(custom.trim())}
          >
            保存
          </button>
        </div>
      )}

      <p className="chart-note">
        选定某一个源时，它不通会自动回落到 GitHub 直连 —— 代理挂掉该是重试一次，而不是装不上插件。
        「直连 GitHub」则不会绕道任何第三方。下载完成后，任务条会写明这一次实际是从哪里下的。
      </p>
    </section>
  )
}
