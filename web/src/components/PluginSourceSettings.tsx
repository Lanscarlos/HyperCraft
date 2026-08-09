import { useState } from 'react'

import type { PluginController } from '../usePlugins'
import { Page } from './Page'

/**
 * Where plugins come from: the credential private repositories are read with,
 * and the proxy their jars are downloaded through.
 *
 * These live in 设置 rather than on the plugin library page for the same reason
 * the panel's update mirror does — they are configured once, on the day the
 * panel is set up or the day something stops working, while the library page is
 * somewhere an operator goes weekly to actually install things. A settings form
 * sitting under the list of plugins is a settings form in everybody's way.
 */
export function PluginSourceSettings({ plugins }: { plugins: PluginController }) {
  const { library, busy } = plugins

  return (
    <Page
      title="插件源"
      lead="插件都来自 GitHub Release。这里管两件事：私有仓库要用的访问令牌，以及 jar 走哪个下载源 —— 两个都只影响下载，插件本身、版本和更新在「资源库 → 插件库」里管，装到某台服上则在那台服的「已装插件」页。"
    >

      <GitHubTokenPanel
        configured={library?.tokenConfigured ?? false}
        hint={library?.tokenHint}
        busy={busy}
        onSave={(token) => plugins.setToken(token)}
      />

      <MirrorPanel
        mirrors={library?.mirrors ?? []}
        current={library?.mirror ?? 'auto'}
        busy={busy}
        onChange={(mirror) => plugins.setMirror(mirror)}
      />

      {plugins.error && <div className="alert alert--error">{plugins.error}</div>}
    </Page>
  )
}

/**
 * The credential private repositories are read with.
 *
 * Write-only on purpose: the panel will say whether it holds a token and show
 * its last four characters, and that is all — a token that can be read back out
 * of a page is a token that leaks with the page. Fixing a wrong one is done by
 * pasting a new one over it, not by editing it in place.
 */
function GitHubTokenPanel({
  configured,
  hint,
  busy,
  onSave,
}: {
  configured: boolean
  hint?: string
  busy: boolean
  onSave: (token: string) => Promise<boolean>
}) {
  const [token, setToken] = useState('')

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    if (!token.trim()) return
    if (await onSave(token.trim())) setToken('')
  }

  return (
    <section className="panel">
      <div className="chart-head">
        <h2 className="panel__title">GitHub 访问令牌</h2>
        {configured && <span className="badge badge--ok">已配置{hint && ` ···${hint}`}</span>}
      </div>
      <p className="chart-note">
        自己写的插件发在私有仓库里时，面板得先能证明「我是你」才看得见它 —— 填一个令牌就行，
        剩下的不用管：哪个仓库是私有的由面板自己问 GitHub，检查更新和下载会自动走带认证的 API。
        顺带一提，就算全是公开仓库，配了令牌也值：匿名调用每小时只有 60 次，插件一多就不够用。
      </p>
      <form onSubmit={(event) => void save(event)}>
        <label className="field">
          <span>令牌</span>
          <input
            type="password"
            value={token}
            autoComplete="off"
            spellCheck={false}
            placeholder={configured ? '粘贴新令牌以替换现有的' : 'github_pat_… 或 ghp_…'}
            onChange={(e) => setToken(e.target.value)}
          />
          <small>
            在 GitHub 的 Settings → Developer settings → Personal access tokens 里生成。
            fine-grained 令牌只要给目标仓库的 <code>Contents: Read-only</code> 权限；
            classic 令牌勾 <code>repo</code>。令牌存在面板自己的 panel.json 里（0600），
            只发给 api.github.com，不会经过下载源。
          </small>
        </label>
        <div className="field__tools">
          <button className="btn btn--primary" type="submit" disabled={busy || !token.trim()}>
            {configured ? '替换令牌' : '保存令牌'}
          </button>
          {configured && (
            <button
              className="btn"
              type="button"
              disabled={busy}
              onClick={() => {
                if (window.confirm('清除后，私有仓库的插件将无法检查更新或下载。确定吗？')) {
                  void onSave('')
                }
              }}
            >
              清除
            </button>
          )}
        </div>
      </form>
    </section>
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
