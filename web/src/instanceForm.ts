import type { InstanceInput, InstanceStatus } from './types'

/**
 * The whole editable config, read out of an instance.
 *
 * Shared by the two pages that edit it — 启动方式 and 实例设置 — and shared
 * on purpose. The API's PUT builds a complete config from the body and takes
 * the zero value for anything absent, so a page that sent only its own half
 * would blank the other page's fields: a heap change would wipe the instance
 * name, a rename would wipe the JVM arguments. Both pages therefore read the
 * whole thing, edit their part of it, and send the whole thing back.
 *
 * See internal/api/handlers_instances.go — toConfig, and the comment above
 * launchFields.
 */
export function toInput(instance: InstanceStatus): InstanceInput {
  return {
    name: instance.name,
    directory: instance.directory,
    loader: instance.loader ?? '',
    gameVersion: instance.gameVersion ?? '',
    java: instance.java,
    jar: instance.jar,
    minMemoryMB: instance.minMemoryMB,
    maxMemoryMB: instance.maxMemoryMB,
    jvmArgs: instance.jvmArgs ?? [],
    serverArgs: instance.serverArgs ?? [],
    argFiles: instance.argFiles ?? [],
    encoding: instance.encoding || 'auto',
    tty: instance.tty ?? true,
    forceColor: instance.forceColor ?? true,
    autoStart: instance.autoStart,
    autoRestart: instance.autoRestart,
    stopCommand: instance.stopCommand,
    stopTimeoutSec: instance.stopTimeoutSec,
  }
}

/** Args are edited as one-per-line text, which is far easier than a list UI. */
export const toLines = (args: string[]) => args.join('\n')

export const fromLines = (text: string) =>
  text
    .split('\n')
    .map((line) => line.trim())
    .filter(Boolean)

/** Which fields differ between what is stored and what would be sent.
 *
 *  Field by field rather than JSON.stringify on the two objects: the payload
 *  is a spread with keys reassigned, and leaning on a spread to preserve key
 *  order for a string comparison is a bug waiting for someone to reorder
 *  toInput(). */
export function changedKeys(
  stored: InstanceInput,
  pending: InstanceInput,
): (keyof InstanceInput)[] {
  return (Object.keys(stored) as (keyof InstanceInput)[]).filter((key) => {
    const a = stored[key]
    const b = pending[key]
    if (Array.isArray(a) && Array.isArray(b)) {
      return a.length !== b.length || a.some((item, at) => item !== b[at])
    }
    return a !== b
  })
}
