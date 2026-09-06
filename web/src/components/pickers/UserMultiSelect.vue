<script setup lang="ts">
import { onMounted } from 'vue'
import { useUsers } from '@/composables/useUsers'
import { useAuthStore } from '@/stores/auth'

defineProps<{ labelledby?: string }>()
const modelValue = defineModel<string[]>({ required: true })
const { users, loading, list } = useUsers()
const auth = useAuthStore()

onMounted(async () => {
  await list()
  // Pre-select the current user by default (only when nothing is selected yet, so this never
  // overrides an existing selection). Matched by name against the OIDC email claim — the only
  // identity link available, since no platform User row is actually linked to the OIDC identity
  // that logged in (see auth store's `email` getter for why this is email, not
  // preferred_username or displayName). Silently does nothing if there's no matching User or
  // nobody is logged in; the checkbox stays a normal, uncheckable-again checkbox either way.
  if (modelValue.value.length === 0 && auth.email) {
    const currentUser = users.value.find((u) => u.name === auth.email)
    if (currentUser) {
      modelValue.value = [currentUser.id]
    }
  }
})

function toggle(id: string) {
  if (modelValue.value.includes(id)) {
    modelValue.value = modelValue.value.filter((existing) => existing !== id)
  } else {
    modelValue.value = [...modelValue.value, id]
  }
}
</script>

<template>
  <div role="group" :aria-labelledby="labelledby" class="max-h-40 overflow-y-auto rounded-md border border-slate-200 p-2">
    <p v-if="loading" class="text-xs text-slate-400">Loading users…</p>
    <label v-for="user in users" :key="user.id" class="flex items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-50">
      <input type="checkbox" :checked="modelValue.includes(user.id)" @change="toggle(user.id)" />
      {{ user.name }}
    </label>
    <p v-if="!loading && users.length === 0" class="text-xs text-slate-400">No Users yet — create one first.</p>
  </div>
</template>
