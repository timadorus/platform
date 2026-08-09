interface ProblemLike {
  title?: string
  detail?: string
  status?: number
}

// problemMessage extracts a human-readable message from a failed API call's error value.
// Every composable's mutating action (create/rename/archive/...) passes its `error` result
// here before showing it via ErrorBanner — never a raw error object or a silent failure.
export function problemMessage(err: unknown): string | null {
  if (!err || typeof err !== 'object') return null
  const p = err as ProblemLike
  return p.detail ?? p.title ?? null
}
