import { useEffect, useRef, useState } from 'react'

import {
  formatServerArgs,
  knownServerFlag,
  parseServerArg,
  parseServerArgs,
  reflowServerArg,
  suggestServerFlags,
  type ServerArg,
} from '../serverFlags'
import { ArgComplete, ArgGrid, ArgRemove } from './ArgShell'
import { Card } from './Card'
import { Section } from './Section'

interface Props {
  text: string
  onText: (text: string) => void
  count: number
  rows: boolean
  onView: (rows: boolean) => void
  proxy: boolean
}

/**
 * The arguments that go after the jar, one card each.
 *
 * Cards for the same reason the JVM arguments got them, and the card/text
 * switch for a reason specific to this one: the old version showed a --nogui
 * chip and a textarea containing --nogui, both live, and the honest answer to
 * "which one do I change" was "either", which is not an answer anybody
 * believes. Two views of one value, one at a time.
 *
 * The text is still the truth — this parses the same one-per-line string the
 * textarea holds and writes the same string back — and a line the vocabulary
 * does not recognise round-trips exactly as typed.
 */
export function ServerArgsCard({ text, onText, count, rows, onView, proxy }: Props) {
  const [args, setArgs] = useState<ServerArg[]>(() => parseServerArgs(text))
  /** The text this component last produced. Anything else arriving in `text`
   *  came from outside — a script import, a different instance — and replaces
   *  the cards wholesale. */
  const emitted = useRef(text)
  const [editing, setEditing] = useState<string | null>(null)
  const focusing = useRef<string | null>(null)

  useEffect(() => {
    if (text === emitted.current) return
    emitted.current = text
    setArgs(parseServerArgs(text))
    setEditing(null)
  }, [text])

  const push = (next: ServerArg[]) => {
    const out = formatServerArgs(next)
    emitted.current = out
    setArgs(next)
    onText(out)
  }

  const patch = (id: string, changes: Partial<ServerArg>) =>
    push(args.map((arg) => (arg.id === id ? reflowServerArg(arg, changes) : arg)))

  const remove = (id: string) => push(args.filter((arg) => arg.id !== id))

  const edit = (id: string) => {
    focusing.current = id
    setEditing(id)
  }

  const add = () => {
    const arg = parseServerArg('')
    edit(arg.id)
    // Not through push(): an empty card is not an argument yet, and emitting
    // it would put a blank line into the value on every 添加参数 press.
    setArgs([...args, arg])
  }

  const commit = (id: string, raw: string) => {
    setEditing(null)
    push(
      args
        .map((arg) => (arg.id === id ? { ...parseServerArg(raw), id: arg.id } : arg))
        .filter((arg) => arg.raw !== ''),
    )
  }

  return (
    <Section
      form
      title={
        <>
          <span className="originmark originmark--server" aria-hidden="true" />
          服务端参数
        </>
      }
      count={count > 0 ? count : undefined}
      meta="跟在 jar 之后"
      note={
        proxy
          ? 'Velocity 遇到不认识的参数会直接退出，一般这里留空。'
          : '传给服务端自己的参数，不是给 JVM 的。'
      }
      tools={
        <div className="presets__view" role="group" aria-label="服务端参数的显示方式">
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
      }
    >
      {rows ? (
        <ArgGrid
          onAdd={add}
          addLabel="+ 添加参数"
          emptyNote={
            proxy ? '代理端通常不需要参数' : '常用的是 --nogui，点一下开始输入会有提示'
          }
          isEmpty={args.length === 0}
          footnote={
            <>
              面板不认识的参数照样能加，会原样保存。参数会放在 <code>jar</code> 之后。
            </>
          }
        >
          {args.map((arg) => (
            <ServerArgCard
              key={arg.id}
              arg={arg}
              editing={editing === arg.id}
              focusing={focusing}
              proxy={proxy}
              onEdit={() => edit(arg.id)}
              onCommit={(raw) => commit(arg.id, raw)}
              onPatch={(changes) => patch(arg.id, changes)}
              onRemove={() => remove(arg.id)}
            />
          ))}
        </ArgGrid>
      ) : (
        <>
          <textarea
            rows={3}
            value={text}
            onChange={(e) => onText(e.target.value)}
            placeholder={proxy ? '' : '--nogui'}
            aria-label="服务端参数"
          />
          <small>
            一行一个参数，会放在 jar 之后。
            {proxy
              ? ' Velocity 遇到不认识的参数会直接退出，一般这里留空。'
              : ' --nogui 关掉服务端自带的那个 Swing 窗口，无头机器上基本都要。'}
          </small>
        </>
      )}
    </Section>
  )
}

function ServerArgCard({
  arg,
  editing,
  focusing,
  proxy,
  onEdit,
  onCommit,
  onPatch,
  onRemove,
}: {
  arg: ServerArg
  editing: boolean
  focusing: React.MutableRefObject<string | null>
  proxy: boolean
  onEdit: () => void
  onCommit: (raw: string) => void
  onPatch: (changes: Partial<ServerArg>) => void
  onRemove: () => void
}) {
  const known = knownServerFlag(arg)

  // Free text: a card being retyped, and every argument this panel cannot
  // classify. The second is not a failure state — a modpack's own launch
  // argument lives there permanently and is none the worse for it.
  if (editing || !known) {
    return (
      <Card pad="tight" tone="sunken" className="jvmcard jvmcard--full">
        <ArgComplete
          id={arg.id}
          raw={arg.raw}
          editing={editing}
          focusing={focusing}
          placeholder="--nogui"
          ariaLabel="服务端参数"
          suggest={(draft) => suggestServerFlags(draft, proxy)}
          onEdit={onEdit}
          onCommit={onCommit}
          trailing={<ArgRemove label={arg.raw} onRemove={onRemove} />}
        />
        {!editing && (
          <div className="jvmcard__note">面板不认识这个参数，按原文保存。</div>
        )}
      </Card>
    )
  }

  return (
    <Card pad="tight" tone="sunken" className="jvmcard">
      <div className="jvmcard__head">
        <button
          type="button"
          className="jvmcard__name"
          onClick={onEdit}
          title="改这一行的原文"
        >
          {arg.name}
        </button>
        <ArgRemove label={arg.raw} onRemove={onRemove} />
      </div>

      <div className="jvmcard__note">{known.note}</div>

      {known.takesValue && (
        <div className="jvmcard__control">
          <input
            type="text"
            className="input-slim"
            value={arg.value}
            spellCheck={false}
            placeholder={known.placeholder}
            aria-label={`${arg.name} 的值`}
            onChange={(e) => onPatch({ value: e.target.value })}
          />
        </div>
      )}
    </Card>
  )
}
