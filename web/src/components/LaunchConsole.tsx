import type { LaunchIssue, LaunchPreview, LaunchSegment, StartupDraft } from '../types'
import { Button } from './Button'
import { Section } from './Section'

/** What each stretch of the command line is, for the legend and for the
 *  squares on the cards that produce it. The order is the order the JVM
 *  receives them. */
const ORIGINS: { id: LaunchSegment['origin']; label: string }[] = [
  { id: 'memory', label: '内存' },
  { id: 'panel', label: '面板注入' },
  { id: 'jvm', label: 'JVM 参数' },
  { id: 'jar', label: '核心' },
  { id: 'server', label: '服务端参数' },
]

/** How each level of finding is introduced. Named for what it means to the
 *  person about to press 启动, not for a severity scale. */
const LEVEL_LABELS: Record<LaunchIssue['level'], string> = {
  fatal: '起不来',
  warn: '能起来，但有问题',
  info: '提示',
  ok: '没问题',
}

interface Props {
  preview: LaunchPreview | null
  stale: boolean
  failed: boolean
  onApplyFix: (patch: Partial<StartupDraft>) => void
}

/**
 * The right-hand column: the command this page is about to produce, and what
 * the panel already knows is wrong with it.
 *
 * The page's whole claim is that the command shown is the command run, which
 * is why the argv comes off the daemon rather than being assembled here — and
 * why the flags the panel injects for its own console are in the list, dimmed
 * and labelled, rather than quietly omitted. Omitting them would make the
 * claim false for every instance in UTF-8 mode, which is all of them.
 *
 * Colour lives in the 7px square at the head of each line and nowhere else.
 * Tinting the argv itself would need five new --term-* tokens across eight
 * theme blocks, and a missed one is an empty value in dark mode.
 */
export function LaunchConsole({ preview, stale, failed, onApplyFix }: Props) {
  const issues = preview?.issues ?? []
  const pending = issues.filter((issue) => issue.level !== 'ok').length

  return (
    <>
      <Section
        title="实际执行的命令"
        note="改左边任何一项，这条命令跟着变。启动时用的就是它。"
      >
        {preview && preview.segments.length > 0 ? (
          <div className={`launchcmd${stale ? ' launchcmd--stale' : ''}`}>
            <div className="launchcmd__line">
              <span className="originmark" aria-hidden="true" />
              <span className="launchcmd__args">{preview.program || 'java'}</span>
            </div>
            {preview.segments.map((segment, at) => (
              <div
                key={`${segment.origin}-${at}`}
                className={`launchcmd__line${
                  segment.origin === 'panel' ? ' launchcmd__line--muted' : ''
                }`}
              >
                <span
                  className={`originmark originmark--${segment.origin}`}
                  aria-hidden="true"
                />
                <span className="launchcmd__args">{segment.args.join(' ')}</span>
              </div>
            ))}
            <div className="launchcmd__legend">
              {ORIGINS.filter((origin) =>
                preview.segments.some((segment) => segment.origin === origin.id),
              ).map((origin) => (
                <span key={origin.id} className="launchcmd__key">
                  <span
                    className={`originmark originmark--${origin.id}`}
                    aria-hidden="true"
                  />
                  {origin.label}
                </span>
              ))}
            </div>
          </div>
        ) : (
          <p className="muted">
            还没有可执行的命令——先在左边指定一个服务端 jar 或参数文件。
          </p>
        )}

        {failed && (
          <small className="muted">
            预览暂时没能更新，上面是上一次的结果。这不影响保存。
          </small>
        )}

        <small className="muted">
          「面板注入」那组来自「实例设置 → 控制台」的编码和颜色开关，不是在这一页填的。
        </small>
      </Section>

      <Section
        title="启动前检查"
        count={pending > 0 ? `${pending} 条待处理` : undefined}
        note="按下「启动」之前，面板能先看出来的问题。"
      >
        {issues.length > 0 ? (
          <ul className="launchcheck">
            {issues.map((issue) => (
              <li
                key={issue.code}
                className={`launchcheck__item launchcheck__item--${issue.level}`}
              >
                <strong className="launchcheck__level">{LEVEL_LABELS[issue.level]}</strong>
                <p className="launchcheck__text">{issue.message}</p>
                {/* The patch is applied as it arrives — no branch on the code.
                    That is what lets a new check ship without touching this
                    file. */}
                {issue.fix && (
                  <div className="actions">
                    <Button
                      size="row"
                      type="button"
                      onClick={() => onApplyFix(issue.fix!.patch)}
                    >
                      {issue.fix.label}
                    </Button>
                  </div>
                )}
              </li>
            ))}
          </ul>
        ) : (
          <p className="muted">还没有检查结果。</p>
        )}
      </Section>
    </>
  )
}
