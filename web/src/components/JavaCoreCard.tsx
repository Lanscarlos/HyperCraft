import type { ReactNode } from 'react'

import { formatBytes } from '../format'
import type { JarInfo } from '../types'
import { Button } from './Button'
import { FieldHelp } from './FieldHelp'
import { Section } from './Section'
import { Select } from './Select'

interface Props {
  argFileMode: boolean
  onMode: (argFile: boolean) => void
  /** Built by the page, because where it sits depends on the mode. */
  javaField: ReactNode
  jar: string
  jars: JarInfo[]
  onJar: (value: string) => void
  argFileText: string
  onArgFileText: (value: string) => void
  onImportScript: () => void
  /** The core library picker, opened from beside the jar it fills in. */
  corePicker: ReactNode
}

/**
 * Which Java runs which launch target.
 *
 * The mode switch is two chips in the card head rather than the two 96px
 * cards it used to be: it is a switch between two shapes of the same answer,
 * and it was taking more room than either shape's fields.
 */
export function JavaCoreCard({
  argFileMode,
  onMode,
  javaField,
  jar,
  jars,
  onJar,
  argFileText,
  onArgFileText,
  onImportScript,
  corePicker,
}: Props) {
  return (
    <Section
      form
      title={
        <>
          <span className="originmark originmark--jar" aria-hidden="true" />
          Java 与核心
        </>
      }
      note={
        argFileMode
          ? 'Forge / NeoForge 没有可以直接跑的 jar，安装器留下的是一组参数文件。'
          : '用哪个 Java、跑哪个 jar —— 这是一个决定的两半。'
      }
      tools={
        <div className="presets__view" role="group" aria-label="启动方式">
          <button
            className={`chip${argFileMode ? '' : ' chip--on'}`}
            type="button"
            aria-pressed={!argFileMode}
            onClick={() => onMode(false)}
          >
            核心 jar
          </button>
          <button
            className={`chip${argFileMode ? ' chip--on' : ''}`}
            type="button"
            aria-pressed={argFileMode}
            onClick={() => onMode(true)}
          >
            参数文件
          </button>
        </div>
      }
    >
      {argFileMode ? (
        <>
          {javaField}

          <label className="field">
            <span>参数文件</span>
            <textarea
              rows={3}
              value={argFileText}
              onChange={(e) => onArgFileText(e.target.value)}
              placeholder={
                'user_jvm_args.txt\nlibraries/net/minecraftforge/forge/1.20.1-47.2.0/unix_args.txt'
              }
              spellCheck={false}
            />
            <small>
              一行一个，路径从实例目录算起，面板会按顺序拼成
              <code> java @第一个 @第二个 …</code>。
            </small>
            <FieldHelp summary="为什么 Forge 没有 jar？">
              Forge 和 NeoForge 从 1.17 起就没有可以直接跑的 jar 了，安装器留下的就是这两个文件
              —— 照 <code>run.sh</code> 里那行抄过来即可。
            </FieldHelp>
          </label>

          <div className="actions">
            <Button type="button" onClick={onImportScript}>
              从启动脚本读参数…
            </Button>
          </div>
        </>
      ) : (
        /* 用哪个 Java、跑哪个 jar — one decision, one row. Stacked, each was a
           380px control ending at the same place, with the rest of the line
           empty beside it. */
        <div className="field-row">
          {javaField}

          <label className="field field--md">
            <span>服务端 jar</span>
            <Select
              allowCustom
              ariaLabel="服务端 jar"
              value={jar}
              placeholder="server.jar"
              options={jars.map((entry) => ({
                value: entry.name,
                label: entry.name,
                note: formatBytes(entry.size),
              }))}
              onChange={onJar}
            />
            {/* No "found N jars" line here any more: it is a check now, in the
                right-hand column, where it sits beside the other things the
                panel looked at rather than under the control it describes. */}
          </label>
        </div>
      )}

      {corePicker}
    </Section>
  )
}
