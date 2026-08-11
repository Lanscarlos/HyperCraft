import { Component, type ErrorInfo, type ReactNode } from 'react'

/**
 * The last thing between one broken page and a blank panel.
 *
 * React unmounts the whole tree when a render throws, so before this existed a
 * single bad payload — a nil slice arriving as null and something reading
 * `.length` off it — took the entire panel white, console and power controls
 * with it. The shell stays up now: whatever else is wrong, the operator can
 * still reach the server that is running.
 *
 * Deliberately not a retry button. Re-rendering the same state throws again;
 * what actually recovers is navigating elsewhere, which the shell now allows,
 * or reloading against fresh data.
 */
interface Props {
  children: ReactNode
  /** Clears the error when it changes — the route, where one is in play. It is
   *  a prop rather than a `key` on purpose: a key would remount the children,
   *  and the children here are the instance's panes, which stay mounted for the
   *  console's websocket and every other pane's loaded state. */
  resetKey?: string
}

interface State {
  error: Error | null
  key?: string
}

export class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null }

  static getDerivedStateFromError(error: Error) {
    return { error }
  }

  static getDerivedStateFromProps(props: Props, state: State): State | null {
    if (props.resetKey === state.key) return null
    return { error: null, key: props.resetKey }
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    // The stack is the only record of what happened: there is no error
    // reporting service to send it to, so leave it where F12 will find it.
    console.error('页面渲染失败', error, info.componentStack)
  }

  render() {
    const { error } = this.state
    if (!error) return this.props.children

    return (
      <div className="panel panel--warn">
        <h2 className="panel__title">这个页面出错了</h2>
        <p className="muted">
          面板的其他部分还能用，从左侧换一个页面就行。如果这一页一直打不开，刷新页面后再试一次。
        </p>
        <div className="alert alert--error">{error.message || String(error)}</div>
        <div className="actions">
          <button className="btn" onClick={() => window.location.reload()}>
            刷新页面
          </button>
        </div>
      </div>
    )
  }
}
