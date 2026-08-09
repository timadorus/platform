<script setup lang="ts">
import { onMounted } from 'vue'
import { useUsers } from '@/composables/useUsers'

const modelValue = defineModel<string[]>({ required: true })
const { users, loading, list } = useUsers()

onMounted(list)

function toggle(id: string) {
  if (modelValue.value.includes(id)) {
    modelValue.value = modelValue.value.filter((existing) => existing !== id)
  } else {
    modelValue.value = [...modelValue.value, id]
  }
}
</script>

<template>
  <div class="max-h-40 overflow-y-auto rounded-md border border-slate-200 p-2">
    <p v-if="loading" class="text-xs text-slate-400">Loading users…</p>
    <label v-for="user in users" :key="user.id" class="flex items-center gap-2 rounded px-1 py-1 text-sm hover:bg-slate-50">
      <input type="checkbox" :checked="modelValue.includes(user.id)" @change="toggle(user.id)" />
      {{ user.name }}
    </label>
    <p v-if="!loading && users.length === 0" class="text-xs text-slate-400">No Users yet — create one first.</p>
  </div>
</template>
