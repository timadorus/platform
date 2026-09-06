// usePolling.ts extracts the poll-with-timeout skeleton this codebase's eventual-consistency waits
// (waitForUser, waitForCharacter, waitForCharacterInList, waitForEntityInList, and now
// waitForCampaign) all shared as near-identical copies — see docs/BACKLOG.md's "poll-with-timeout
// pattern is now duplicated" entry.
export interface PollOptions {
  intervalMs?: number
  timeoutMs?: number
  signal?: AbortSignal
  // shouldStop, if given, is checked immediately after each attempt (before checking whether it
  // found anything) — for the "list membership" shape, where a reactive error ref getting set is
  // a hard failure that must stop polling right away, distinct from "not found yet, keep going"
  // (which is what a null/undefined attempt result means with no shouldStop signal).
  shouldStop?: () => boolean
}

// pollUntil calls attempt() repeatedly until it returns a non-null/non-undefined value, shouldStop()
// returns true, opts.signal aborts, or timeoutMs elapses. Returns the found value, or null on any
// give-up condition. Checks abort both before and after each attempt call (an in-flight attempt
// that resolves after the caller has already moved on must not be acted on).
export async function pollUntil<T>(
  attempt: () => Promise<T | null | undefined>,
  opts: PollOptions = {},
): Promise<T | null> {
  const intervalMs = opts.intervalMs ?? 750
  const timeoutMs = opts.timeoutMs ?? 15000
  const deadline = Date.now() + timeoutMs
  for (;;) {
    if (opts.signal?.aborted) return null
    const result = await attempt()
    if (opts.signal?.aborted) return null
    if (opts.shouldStop?.()) return null
    if (result !== null && result !== undefined) return result
    if (Date.now() >= deadline) return null
    await new Promise((resolve) => setTimeout(resolve, intervalMs))
  }
}
