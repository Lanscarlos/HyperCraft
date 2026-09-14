import { useCallback, useEffect, useRef, useState } from 'react'

import { api } from '../api'
import { toast } from '../toast'
import { ask } from '../confirm'
import { changedKeys, toInput } from '../instanceForm'
import type { InstanceSection } from '../routes'
import type { InstanceInput, InstanceStatus, LaunchIssue } from '../types'
import { ENCODING_OPTIONS, isLive, LOADER_OPTIONS } from '../types'
import { Button } from './Button'
import { FieldHelp } from './FieldHelp'
import { Note } from './Note'
import { useHostJars } from '../useHostJars'
import { PageHead } from './Page'
import { DirectoryField } from './PathPicker'
import { Section } from './Section'
import { Select } from './Select'

interface Props {
  instance: InstanceStatus
  onSaved: (updated: InstanceStatus) => void
  onDeleted: () => void
  onOpenSection: (section: InstanceSection) => void
}

/**
 * What this server is and how the panel treats it — everything except how it
 * is launched, which is its own page now (see StartupSettings).
 *
 * The split is along "would you open this twice": a name and a stop command
 * are set once and forgotten, while a heap and a GC flag are tuned over and
 * over. Keeping them on one page meant scrolling past the first to reach the
 * second every time.
 */
export function InstanceSettings({ instance, onSaved, onDeleted, onOpenSection }: Props) {
  const [form, setForm] = useState<InstanceInput>(() => toInput(instance))
  const [status, setStatus] = useState<string | null>(null)
  const [error, setError] = useState<string | null>(null)
  const [busy, setBusy] = useState(false)
  // The one thing from the launch check that still belongs on this page.
  //
  // The panel is worth nothing to somebody whose server will not start, and
  // this is the page they open looking for the reason — it is called 实例设置
  // and it used to hold the whole check. It does not any more, so what stays
  // is a pointer: the first fatal finding and the way to the page that can
  // actually fix it. Not the whole panel, which is the rail's job on 启动方式
  // and would be two places to keep saying the same thing.
  const [fatal, setFatal] = useState<LaunchIssue | null>(null)

  // A proxy answers "end" rather than "stop", which is worth saying in the
  // placeholder.
  const proxy = instance.kind === 'proxy'

  // Whether this host has pseudo-terminals at all. A switch that silently
  // falls back is worse than one that is visibly unavailable.
  const ttySupported = instance.ttySupported ?? true

  // Only for the directory field's hint — the jar list itself moved to
  // 启动方式 along with the field that reads it.
  const { exists: directoryExists } = useHostJars(form.directory)

  const stored = toInput(instance)
  const changed = changedKeys(stored, form)
  const dirty = changed.length > 0

  const reseed = useCallback((from: InstanceStatus) => {
    setForm(toInput(from))
  }, [])

  // Both settings pages hold the whole config and PUT the whole thing back, so
  // a page sitting on a stale copy would undo whatever the other one just
  // saved. And they sit on one: the panes stay mounted behind each other, so
  // "启动方式 saved while this page was off screen" is the normal case, not an
  // exotic one. Re-seed whenever the stored config moved and this form has
  // nothing of its own to lose.
  //
  // Keyed on the stored values rather than on instance.id: the id never
  // changes within a mount — App keys the whole view on it — so an id-keyed
  // effect would never fire at all.
  const storedKey = JSON.stringify(stored)
  const seeded = useRef(storedKey)
  useEffect(() => {
    if (seeded.current === storedKey) return
    seeded.current = storedKey
    if (dirty) return
    reseed(instance)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [storedKey])

  // Re-read after every save: a fatal finding is usually about a file on disk,
  // and the save may have been the thing that fixed it.
  useEffect(() => {
    let cancelled = false
    api
      .launchCheck(instance.id)
      .then((check) => {
        if (cancelled) return
        setFatal(check.issues.find((issue) => issue.level === 'fatal') ?? null)
      })
      .catch(() => {
        if (!cancelled) setFatal(null)
      })
    return () => {
      cancelled = true
    }
  }, [instance.id, storedKey])

  // Back to what is stored.
  const revert = () => {
    reseed(instance)
    setError(null)
    setStatus(null)
  }

  const update = <K extends keyof InstanceInput>(key: K, value: InstanceInput[K]) =>
    setForm((prev) => ({ ...prev, [key]: value }))

  const save = async (event: React.FormEvent) => {
    event.preventDefault()
    setBusy(true)
    setError(null)
    setStatus(null)
    try {
      // The whole config, not just this page's half: an absent field is a
      // zero value to the daemon, so sending only these four sections would
      // blank the jar, the heap and every JVM argument. See instanceForm.
      onSaved(await api.updateInstance(instance.id, form))
      toast(isLive(instance.state) ? '已保存，将在下次启动时生效' : '已保存', {
        key: 'instance-settings.save',
      })
    } catch (err) {
      setError(err instanceof Error ? err.message : '保存失败')
    } finally {
      setBusy(false)
    }
  }

  const remove = async (deleteFiles: boolean) => {
    const ok = await ask(
      deleteFiles
        ? {
            title: `删除实例「${instance.name}」并抹掉它的文件？`,
            lead: (
              <>
                目录 <code>{instance.directory}</code> 下的所有文件都会被永久删除，存档也在里面。
              </>
            ),
            detail:
              '此操作不可撤销，面板没有为它留回收站。配置历史也会一并删除 —— 那是唯一还能' +
              '看到旧配置的地方。要保留文件请改用「从面板移除」。',
            confirmLabel: '删除实例和文件',
            danger: true,
          }
        : {
            title: `从面板移除实例「${instance.name}」？`,
            lead: '面板不再管理它，列表里也不会再出现。',
            detail: (
              <>
                服务器文件原样留在 <code>{instance.directory}</code>，之后可以用「导入现有目录」再加回来。
                这台实例的配置历史会被删除，它存在面板的数据目录里而不是服务器目录里。
              </>
            ),
            confirmLabel: '移除',
          },
    )
    if (!ok) return

    setBusy(true)
    try {
      await api.deleteInstance(instance.id, deleteFiles)
      onDeleted()
    } catch (err) {
      setError(err instanceof Error ? err.message : '删除失败')
      setBusy(false)
    }
  }

  return (
    <form className="stack stack--narrow" onSubmit={save}>
      <PageHead
        title="实例设置"
        lead="名称、目录、控制台编码，以及面板什么时候替你开关机。怎么启动在「启动方式」那页。"
      />

      {/* A Note and not an .alert: this page loaded fine. What is wrong is the
          instance, which is a condition that is true right now — the test the
          design system gives is whether it renders from an `if`. */}
      {fatal && (
        <Note tone="error">
          <span>{fatal.message}</span>
          <Button size="row" type="button" onClick={() => onOpenSection('startup')}>
            去启动方式
          </Button>
        </Note>
      )}

      <Section form title="基本信息" note="这台服务器叫什么、是什么服务端、文件放在哪。">
      {/* Who it is, on one line: a name, a kind and a version are one answer,
          and each of the three was a row of its own ending at a different
          place. 服务端类型 is a short identifier (Paper, NeoForge,
          CraftBukkit) rather than a name, so it takes the short measure and
          the three fit the reading width together. */}
      <div className="field-row">
        <label className="field field--md">
          <span>实例名称</span>
          <input
            value={form.name}
            onChange={(e) => update('name', e.target.value)}
            required
          />
        </label>
        <label className="field field--sm">
          <span>服务端类型</span>
          <Select
            ariaLabel="服务端类型"
            value={form.loader}
            options={LOADER_OPTIONS.map((entry) => ({
              value: entry.value,
              label: entry.label,
              note: entry.note,
            }))}
            onChange={(next) => update('loader', next)}
          />
        </label>
        <label className="field field--sm">
          <span>游戏版本</span>
          <input
            value={form.gameVersion}
            onChange={(e) => update('gameVersion', e.target.value)}
            placeholder="1.20.1"
            spellCheck={false}
          />
        </label>
      </div>

      <p className="muted">
        面板先从目录和 jar 名认，认不出来才用这里填的。
        <FieldHelp summary="哪些认不出来？">
          <strong>Forge 这类认不出来</strong> —— 没有 jar 名可读，
          <code>version_history.json</code> 也只有 Paper 系才写。认不出来的后果很具体：
          mod 会被装进 <code>plugins/</code> 而不是 <code>mods/</code>，插件市场里每一条也都标成「未知」。
        </FieldHelp>
      </p>

      <DirectoryField
        value={form.directory}
        onChange={(value) => update('directory', value)}
        disabled={isLive(instance.state)}
        hint={
          <>
            服务端 jar、存档和配置都放在这里。「浏览…」可以指到本机任意位置，
            包括一个已经有服务端的目录。
            {!directoryExists && ' 这个目录还不存在，保存后会在启动时创建。'}
            {isLive(instance.state) && ' 服务器运行时无法修改。'}
          </>
        }
      />

      </Section>

      <Section form title="控制台" note="网页控制台怎么读服务端的输出、怎么把命令送回去。">
      <label className="field field--md">
        <span>输出编码</span>
        <Select
          ariaLabel="输出编码"
          value={form.encoding}
          options={ENCODING_OPTIONS.map((option) => ({
            value: option.value,
            label: option.label,
          }))}
          onChange={(next) => update('encoding', next)}
        />
        <small>控制台按这个编码解读服务器输出、并按同样的编码发送命令。</small>
        <FieldHelp summary="乱码了怎么办？">
          「自动」会让 JVM 用
          UTF-8 输出，同时对不是 UTF-8 的行按系统编码兜底。用自己的脚本启动时，
          「让 JVM 用 UTF-8」这半件事要靠上面那个 <code>JAVA_TOOL_OPTIONS</code> 开关；
          那个关着、中文 Windows 上又乱码的话，这里改成 GBK 通常就好了。
        </FieldHelp>
      </label>

      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.tty && ttySupported}
          disabled={!ttySupported}
          onChange={(e) => update('tty', e.target.checked)}
        />
        <div className="checkbox__text">
          <span>使用终端模式（推荐）</span>
          {ttySupported ? (
            <>
              <small>Tab 补全由正在运行的服务端回答，进度条不用等换行就能看到。</small>
              <FieldHelp>
                把服务器跑在伪终端上，就像你自己在 SSH 里开着它一样。这样 Tab 补全由
                <strong>正在运行的服务端</strong>回答（插件命令、真实玩家名都算数），
                进度条不用等换行就能看到，颜色也不需要强制。代价是终端只有一条流，
                stderr 不再单独标红。关掉则回到管道模式。
              </FieldHelp>
            </>
          ) : (
            <small>本系统没有可用的伪终端（Windows 需要 ConPTY），所有实例都以管道模式运行。</small>
          )}
        </div>
      </label>

      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.forceColor}
          disabled={form.tty && ttySupported}
          onChange={(e) => update('forceColor', e.target.checked)}
        />
        <div className="checkbox__text">
          <span>强制彩色输出（推荐）</span>
          <small>仅在管道模式下有意义，终端模式下这两个参数不会被加上。</small>
          <FieldHelp>
            服务端只在检测到终端时才上色，所以管道模式会加上
            <code> -Dterminal.jline=false -Dterminal.ansi=true</code>，让网页控制台和
            cmd 里一样有颜色。终端模式下服务端本来就看得到终端，这两个参数不会被加上
            —— <code>terminal.jline=false</code> 恰好会关掉终端模式想要的那个补全。
            用自己的脚本启动时，这两个参数走
            <code> JAVA_TOOL_OPTIONS</code> 送进去，要在上面把那个开关留着。
          </FieldHelp>
        </div>
      </label>
      </Section>

      <Section form title="进程管理" note="面板什么时候替你开服、什么时候替你重启、怎么停。">
      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.autoStart}
          onChange={(e) => update('autoStart', e.target.checked)}
        />
        <span>面板启动时自动启动该服务器</span>
      </label>

      <label className="checkbox">
        <input
          type="checkbox"
          checked={form.autoRestart}
          onChange={(e) => update('autoRestart', e.target.checked)}
        />
        <span>崩溃后自动重启（连续失败 5 次后放弃）</span>
      </label>

      <div className="field-row">
        <label className="field field--md">
          <span>停服命令</span>
          <input
            value={form.stopCommand}
            onChange={(e) => update('stopCommand', e.target.value)}
            placeholder={proxy ? 'end' : 'stop'}
          />
        </label>
        <label className="field field--num">
          <span>停服超时 (秒)</span>
          <input
            type="number"
            min={1}
            value={form.stopTimeoutSec}
            onChange={(e) => update('stopTimeoutSec', Number(e.target.value))}
          />
          <small>超时后发送终止信号，再等 15 秒强制结束。</small>
        </label>
      </div>
      </Section>

      {error && <div className="alert">{error}</div>}
      {status && <Note tone="ok">{status}</Note>}

      {dirty && (
        <div className="formbar">
          <span className="formbar__note">
            {changed.length} 项改动待保存
            {isLive(instance.state) ? '，服务器正在运行，下次启动生效' : '，下次启动即生效'}
          </span>
          <Button size="row" type="button" onClick={revert} disabled={busy}>
            放弃
          </Button>
          <Button variant="primary" size="row" type="submit" disabled={busy}>
            {busy ? '保存中…' : '保存设置'}
          </Button>
        </div>
      )}

      <Section
        tone="danger"
        title="危险操作"
        note="这两个都不可撤销，面板没有为它们留回收站。服务器运行时都不可用。"
      >
        <div className="actions">
          <Button
            type="button"
            onClick={() => remove(false)}
            disabled={busy || isLive(instance.state)}
          >
            从面板移除
          </Button>
          <Button
            variant="danger"
            type="button"
            onClick={() => remove(true)}
            disabled={busy || isLive(instance.state)}
          >
            删除实例及所有文件
          </Button>
        </div>
      </Section>
    </form>
  )
}
