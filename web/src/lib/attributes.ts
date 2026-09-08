// Display metadata only (name + abbreviation) — fixed for every Character regardless of Ruleset,
// unlike the per-Character temp/pot/bonus values themselves. Shared by AttributesTable.vue (the
// read-only display) and AssignStatsBudgetModal.vue (the Pot-editing modal it opens), so the two
// can never drift out of sync on which ten attributes exist. Moved here from AttributesTable.vue,
// which used to declare this locally as its own ATTRIBUTES constant.
export const ATTRIBUTES = [
  { name: 'Strength', abbr: 'ST' },
  { name: 'Agility', abbr: 'AG' },
  { name: 'Constitution', abbr: 'CO' },
  { name: 'Quickness', abbr: 'QU' },
  { name: 'Self Discipline', abbr: 'SD' },
  { name: 'Memory', abbr: 'ME' },
  { name: 'Reasoning', abbr: 'RE' },
  { name: 'Empathy', abbr: 'EM' },
  { name: 'Presence', abbr: 'PR' },
  { name: 'Intuition', abbr: 'IN' },
] as const

// potCost computes the statBudget cost of raising a single attribute's Pot from initial to
// target, per the tiered rule from
// docs/superpowers/specs/2026-09-07-assign-stats-budget-design.md Decision 1: the portion of the
// increase at or below Pot 90 costs 1 statBudget point per Pot point; the portion above Pot 90
// costs 5 statBudget points per Pot point. Mirrors potCost in
// internal/engine/timadorus/character_processor.go exactly — the engine is the authority; this
// copy only drives AssignStatsBudgetModal.vue's live "Budget remaining" feedback and blur-time
// validation, both of which the engine independently re-derives and re-checks itself.
export function potCost(initial: number, target: number): number {
  const below = Math.max(0, Math.min(target, 90) - initial)
  const above = Math.max(0, target - Math.max(initial, 90))
  return below + above * 5
}
