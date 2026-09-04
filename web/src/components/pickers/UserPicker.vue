<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useUsers } from '@/composables/useUsers'

const props = defineProps<{ excludeIds?: string[] }>()
const emit = defineEmits<{ select: [id: string] }>()

const { users, list } = useUsers()
const query = ref('')

onMounted(list)

const filtered = computed(() => {
  const excluded = new Set(props.excludeIds ?? [])
  const q = query.value.trim().toLowerCase()
  return users.value
    .filter((u) => !excluded.has(u.id))
    .filter((u) => !q || u.name.toLowerCase().includes(q))
    .slice(0, 20)
})
</script>

<template>
  <div data-testid="user-picker">
    <input v-model="query" type="text" placeholder="Search users…" class="mb-2 w-full rounded-md border border-slate-300 px-2 py-1.5 text-sm" />
    <div class="max-h-40 overflow-y-auto rounded-md border border-slate-200">
      <button
        v-for="u in filtered"
        :key="u.id"
        type="button"
        class="block w-full px-2 py-1.5 text-left text-sm hover:bg-slate-50"
        @click="emit('select', u.id)"
      >
        {{ u.name }}
      </button>
      <p v-if="filtered.length === 0" class="px-2 py-1.5 text-xs text-slate-400">No matches.</p>
    </div>
  </div>
</template>
