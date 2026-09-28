// Copyright 2026 TPT Solutions. Dual-licensed MIT OR Apache-2.0.
import type { FlushOutcome } from './sync'

/** One entry from the daemon's `cortex.task.list` result. */
export type DaemonTask = { state: string }

export type TaskSummary = { queued: number; running: number; completed: number; failed: number }

/** Count daemon tasks by state; unknown states land in `queued`'s bucket neighbours as `other`-safe zeros. */
export function summarizeTasks(tasks: DaemonTask[]): TaskSummary {
  const summary: TaskSummary = { queued: 0, running: 0, completed: 0, failed: 0 }
  for (const task of tasks) {
    switch (task.state) {
      case 'queued':
        summary.queued++
        break
      case 'running':
        summary.running++
        break
      case 'completed':
        summary.completed++
        break
      case 'failed':
        summary.failed++
        break
    }
  }
  return summary
}

/** Human line for the panel: what the last flush did, in plain words. */
export function describeOutcome(outcome: FlushOutcome | null): string {
  if (outcome === null || outcome.via === 'none') return 'nothing to sync yet'
  const units = outcome.synced === 1 ? 'entry' : 'entries'
  if (outcome.via === 'cortex') return `${outcome.synced} ${units} handed to the daemon`
  if (outcome.failed === 0) return `${outcome.synced} ${units} pushed directly`
  const waiting = outcome.pending === 1 ? 'entry waiting' : 'entries waiting'
  return `${outcome.synced} ${units} pushed, ${outcome.pending} ${waiting} for connectivity`
}
