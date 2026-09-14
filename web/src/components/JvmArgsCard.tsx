import { detectPreset, JVM_PRESETS } from '../jvmPresets'
import { Button } from './Button'
import { JVMArgsEditor } from './JVMArgsEditor'
import { Section } from './Section'

interface Props {
  text: string
  onText: (text: string) => void
  count: number
  rows: boolean
  onView: (rows: boolean) => void
  onPick: (id: string) => void
  onImport: () => void
}

/**
 * The flags that go before -jar.
 *
 * The three presets show which one you are on rather than only reacting to
 * being pressed — detected from the arguments themselves, so it survives a
 * reload and is right for flags that arrived by reading somebody's run.sh.
 *
 * The editor stays cards and does not become chips: the cards carry each
 * flag's own control and, more importantly, each line's original text, which
 * is what keeps a one-flag change a one-line diff in 配置历史. See
 * JVMArgsEditor.
 */
export function JvmArgsCard({ text, onText, count, rows, onView, onPick, onImport }: Props) {
  const active = detectPreset(text.split('\n').map((line) => line.trim()).filter(Boolean))

  return (
    <Section
      form
      title={
        <>
          <span className="originmark originmark--jvm" aria-hidden="true" />
          JVM 参数
        </>
      }
      count={count > 0 ? count : undefined}
      note="调垃圾回收和堆行为的那些 -XX 开关。面板不认识的照样能加，会原样保存。"
      tools={
        <>
          <Button size="row" type="button" onClick={onImport}>
            从启动脚本读…
          </Button>
          <div className="presets__view" role="group" aria-label="JVM 参数的显示方式">
            <button
              className={`chip${rows ? ' chip--on' : ''}`}
              type="button"
              aria-pressed={rows}
              onClick={() => onView(true)}
            >
              卡片
            </button>
            <button
              className={`chip${rows ? '' : ' chip--on'}`}
              type="button"
              aria-pressed={!rows}
              onClick={() => onView(false)}
            >
              文本
            </button>
          </div>
        </>
      }
    >
      <div className="presetpick" role="group" aria-label="JVM 参数预设">
        {JVM_PRESETS.map((entry) => (
          <button
            key={entry.id}
            type="button"
            className={`presetpick__one${
              active === entry.id ? ' presetpick__one--on' : ''
            }`}
            aria-pressed={active === entry.id}
            onClick={() => onPick(entry.id)}
          >
            <strong>{entry.label}</strong>
            <small>{entry.note}</small>
          </button>
        ))}
      </div>

      {rows ? (
        <JVMArgsEditor value={text} onChange={onText} />
      ) : (
        <>
          <textarea
            rows={4}
            value={text}
            onChange={(e) => onText(e.target.value)}
            placeholder={'-XX:+UseG1GC\n-XX:MaxGCPauseMillis=200'}
            aria-label="JVM 参数"
          />
          <small>一行一个参数，会放在 -jar 之前。</small>
        </>
      )}

      {/* The Aikar heap warning used to live here as a permanent banner. It is
          a check now — with the button that fixes it — in the right-hand
          column, which is where the other things the panel noticed are. */}
    </Section>
  )
}
