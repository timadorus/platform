import { computed, getCurrentScope, onScopeDispose, reactive, type ComputedRef } from 'vue'

// WatchClause is the exact vocabulary cmd/realtime's GET /changes/stream?watch=... speaks
// (design spec Decision 3) — this list is exhaustive by design, not partial: it's exactly the
// filtering every existing lastAggregateChange watcher already did client-side, plus the two
// new list-level Universe/Campaign clauses this work adds (UniversePickerView/CampaignPickerView).
export type WatchClause =
  | { type: 'universe' }
  | { type: 'universe'; aggregateId: string }
  | { type: 'campaign'; universeId: string }
  | { type: 'campaign'; aggregateId: string }
  | { type: 'character'; campaignId: string }
  | { type: 'character'; aggregateId: string }
  | { type: 'entity'; universeId: string }
  | { type: 'entity'; aggregateId: string }
  | { type: 'object'; universeId: string }
  | { type: 'object'; aggregateId: string }

// registry is a module-level singleton — deliberately not per-call state like every other
// useXxx() composable in this codebase (see this plan's Global Constraints). Components that
// subscribe span multiple top-level routes (UniversePickerView/CampaignPickerView sit outside
// WorkspaceView's subtree entirely), so no single provide()/inject() ancestor high enough in
// the tree exists to hold this instead. reactive(Map), not a plain Map + manual version
// counter — Vue 3's reactive() fully tracks Map mutations (set/delete), so watchedClauses
// below recomputes correctly with no extra bookkeeping.
const registry = reactive(new Map<number, WatchClause>())
let nextId = 0

// watchAggregate registers interest in one WatchClause for as long as the calling component
// stays mounted. Duplicate/overlapping clauses across components are not deduplicated —
// cmd/realtime's Hub.Broadcast evaluates each clause independently and stops at the first
// match, so redundant identical clauses cost a little iteration, never incorrect behavior;
// deduping here would add complexity for no observable benefit (YAGNI).
export function watchAggregate(clause: WatchClause): () => void {
  const id = nextId++
  registry.set(id, clause)
  const unregister = () => {
    registry.delete(id)
  }
  // Every real call site is inside a component's <script setup> (an active effect scope).
  // getCurrentScope() guards this composable for a caller outside one, where auto-cleanup
  // can't apply and the caller becomes responsible for calling the returned function itself.
  if (getCurrentScope()) onScopeDispose(unregister)
  return unregister
}

// watchedClauses is the single source of truth for "what is the SPA currently displaying,
// across every mounted component" — useChangeFeed.ts (Task 3) watches this directly to decide
// when to reopen its EventSource.
export const watchedClauses: ComputedRef<WatchClause[]> = computed(() => Array.from(registry.values()))
