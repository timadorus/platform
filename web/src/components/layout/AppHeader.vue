<script setup lang="ts">
import { useAuthStore } from '@/stores/auth'

defineProps<{
  universeName: string | null
  campaignName: string | null
}>()
const emit = defineEmits<{
  'click-universe-badge': []
  'click-campaign-badge': []
}>()

const auth = useAuthStore()
</script>

<template>
  <header class="flex h-14 flex-shrink-0 items-center gap-3 border-b border-slate-200 bg-white px-4">
    <span class="text-sm font-semibold text-slate-900">Timadorus</span>
    <button
      v-if="universeName"
      class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-700 hover:bg-slate-200"
      @click="emit('click-universe-badge')"
    >
      🌍 {{ universeName }}
    </button>
    <button
      v-if="campaignName"
      class="rounded-full bg-slate-100 px-3 py-1 text-xs text-slate-700 hover:bg-slate-200"
      @click="emit('click-campaign-badge')"
    >
      🎲 {{ campaignName }}
    </button>
    <div class="ml-auto flex items-center gap-2">
      <span class="text-sm text-slate-700">{{ auth.displayName }}</span>
      <router-link to="/users" class="text-slate-400 hover:text-slate-700" title="Manage Users" aria-label="Manage Users">
        ⚙
      </router-link>
      <button class="text-xs text-slate-400 hover:text-slate-700" @click="auth.logout()">Log out</button>
    </div>
  </header>
</template>
