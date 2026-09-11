import type { ParsedScript } from '../types'

/**
 * What was read out of the chosen start script, before anything is created.
 *
 * This is the whole safety argument for parsing somebody's shell with a static
 * reader rather than running it: a wrong reading is visible here, in a list the
 * operator checks, instead of being stored and only showing up as a server that
 * will not start. Which is also why a refusal is shown in full — the line that
 * defeated the parser is more useful than any apology.
 */
export function ScriptDraft({ parsed, parsing }: { parsed: ParsedScript | null; parsing: boolean }) {
  if (parsing) return <p className="muted">正在读这个脚本…</p>
  if (!parsed) return null

  if (!parsed.ok) {
    return (
      <div className="alert alert--error">
        {parsed.script} 拆不出启动参数，面板不猜——下面填一下核心和内存就能导入，脚本留着不动。
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
      </div>
    )
  }

  const { draft } = parsed
  const facts = [
    draft.javaVar ? `Java 来自 $${draft.javaVar}，得自己选一个` : `Java ${draft.java || '（面板选的）'}`,
    draft.minMemoryMB > 0 ? `最小内存 ${draft.minMemoryMB} MB` : '',
    draft.maxMemoryMB > 0 ? `最大内存 ${draft.maxMemoryMB} MB` : '',
    draft.jvmArgs.length > 0 ? `${draft.jvmArgs.length} 个 JVM 参数` : '',
    draft.jar ? `核心 ${draft.jar}` : '',
    draft.argFiles.length > 0 ? `参数文件 ${draft.argFiles.join('、')}` : '',
    draft.serverArgs.length > 0 ? `服务端参数 ${draft.serverArgs.join(' ')}` : '',
  ].filter(Boolean)

  return (
    <div className="alert alert--ok">
      从 {parsed.script} 里读到了这些，导入后在「实例设置 → 启动设置」里都能改。
      <p className="meta-chips">
        {facts.map((fact) => (
          <span key={fact}>{fact}</span>
        ))}
      </p>
      {draft.wrappers && draft.wrappers.length > 0 && (
        <small>
          脚本原来用 <code>{draft.wrappers.join('、')}</code> 把服务端放到后台，
          面板会改成自己前台接管 —— 这样控制台和停止按钮才管用。
        </small>
      )}
      {draft.javaw && (
        <small>脚本用的是 javaw（没有控制台的那个），面板会换成 java，否则控制台收不到输出。</small>
      )}
      {draft.javaVar && (
        <small>
          脚本的 Java 来自环境变量 <code>${draft.javaVar}</code>，面板读不到它的值也不去猜 ——
          导入后在启动设置里选一个 Java。
        </small>
      )}
    </div>
  )
}
