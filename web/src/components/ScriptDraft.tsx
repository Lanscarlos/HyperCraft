import type { ParsedScript } from '../types'
import { Note } from './Note'

/**
 * Where the draft is being read, which decides what of it is news.
 *
 * 导入现有目录 has no instance yet, so everything in the script is worth
 * having — including the Java it named and the jar it ran.
 *
 * 实例设置 is the opposite: by then the panel owns the Java (资源库 → Java
 * 环境) and the jar (核心库), and a year-old run.sh naming /usr/lib/jvm/java-8
 * would undo a deliberate choice made in the panel. So those two are read out
 * and not applied. Saying so beats hiding them — "this server used to run on
 * Java 8" is exactly the sort of thing that explains why it will not start on
 * 25.
 */
export type DraftMode = 'import' | 'settings'

export function ScriptDraft({
  parsed,
  parsing,
  mode = 'import',
}: {
  parsed: ParsedScript | null
  parsing: boolean
  mode?: DraftMode
}) {
  if (parsing) return <p className="muted">正在读这个脚本…</p>
  if (!parsed) return null

  if (!parsed.ok) {
    return (
      <Note tone="error">
        {parsed.script} 拆不出启动参数，面板不猜——
        {mode === 'import'
          ? '下面填一下核心和内存就能导入，脚本留着不动。'
          : '下面的参数还是自己填，脚本留着不动。'}
        <ul className="launchcheck">
          {parsed.refusals.map((refusal) => (
            <li key={refusal.code} className="launchcheck__item launchcheck__item--fatal">
              <p className="launchcheck__text">{refusal.reason}</p>
              {refusal.text && (
                <code className="launchcheck__line">
                  {refusal.line ? `第 ${refusal.line} 行  ` : ''}
                  {refusal.text}
                </code>
              )}
            </li>
          ))}
        </ul>
      </Note>
    )
  }

  const { draft } = parsed
  const settings = mode === 'settings'

  // The jar is the launch target only when there are no argfiles — Forge and
  // NeoForge have no runnable jar at all.
  const jarFact = draft.jar && draft.argFiles.length === 0 ? `核心 ${draft.jar}` : ''
  const javaFact = draft.javaVar
    ? `Java 来自 $${draft.javaVar}，得自己选一个`
    : `Java ${draft.java || '（面板选的）'}`

  const facts = [
    // In 实例设置 these two are listed separately, as things being left alone.
    settings ? '' : javaFact,
    draft.minMemoryMB > 0 ? `最小内存 ${draft.minMemoryMB} MB` : '',
    draft.maxMemoryMB > 0 ? `最大内存 ${draft.maxMemoryMB} MB` : '',
    draft.jvmArgs.length > 0 ? `${draft.jvmArgs.length} 个 JVM 参数` : '',
    settings ? '' : jarFact,
    draft.argFiles.length > 0 ? `参数文件 ${draft.argFiles.join('、')}` : '',
    draft.serverArgs.length > 0 ? `服务端参数 ${draft.serverArgs.join(' ')}` : '',
  ].filter(Boolean)

  // What the settings page reads out but will not apply. Empty for a script
  // that named neither, which is why it is built rather than written out.
  //
  // A bare `java` is left out: it means "whatever is on PATH", which is not a
  // thing the script chose and not news next to a Java the panel installed.
  // What is worth saying is /usr/lib/jvm/java-8/bin/java — the answer to why a
  // server that ran for years will not start on 25.
  const managedJava = settings && draft.java !== '' && draft.java !== 'java'
  const managedJar = settings && jarFact !== ''
  const managed = [managedJava ? `Java ${draft.java}` : '', managedJar ? jarFact : ''].filter(
    Boolean,
  )
  // Only name the place each one is actually managed from, so the parenthetical
  // matches the list above it rather than describing both every time.
  const managedWhere = [
    managedJava ? 'Java 在「资源库 → Java 环境」' : '',
    managedJar ? '核心在核心库' : '',
  ].filter(Boolean)

  return (
    <Note tone="ok">
      从 {parsed.script} 里读到了这些
      {settings ? '，填进表单后还能再改，保存之前不会生效。' : '，导入后在「实例设置 → 启动设置」里都能改。'}
      <p className="meta-chips">
        {facts.map((fact) => (
          <span key={fact}>{fact}</span>
        ))}
      </p>
      {managed.length > 0 && (
        <small>
          脚本里还写了
          {managed.map((fact, index) => (
            <span key={fact}>
              {index > 0 && '、'} <code>{fact}</code>
            </span>
          ))}
          ，面板自己管着这
          {managed.length > 1 ? '两项' : '一项'}（{managedWhere.join('，')}），
          <strong>不会被覆盖</strong>。真要换就在上面对应的框里改。
        </small>
      )}
      {draft.wrappers && draft.wrappers.length > 0 && !settings && (
        <small>
          脚本原来用 <code>{draft.wrappers.join('、')}</code> 把服务端放到后台，
          面板会改成自己前台接管 —— 这样控制台和停止按钮才管用。
        </small>
      )}
      {draft.javaw && !settings && (
        <small>脚本用的是 javaw（没有控制台的那个），面板会换成 java，否则控制台收不到输出。</small>
      )}
      {draft.javaVar && !settings && (
        <small>
          脚本的 Java 来自环境变量 <code>${draft.javaVar}</code>，面板读不到它的值也不去猜 ——
          导入后在启动设置里选一个 Java。
        </small>
      )}
    </Note>
  )
}
