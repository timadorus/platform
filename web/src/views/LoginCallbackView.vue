<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useRouter } from 'vue-router'
import { useAuthStore } from '@/stores/auth'

const router = useRouter()
const auth = useAuthStore()
const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const returnPath = await auth.handleCallback()
    router.replace(returnPath)
  } catch (err) {
    error.value = err instanceof Error ? err.message : 'Sign-in failed.'
  }
})
</script>

<template>
  <div class="flex min-h-screen items-center justify-center text-sm">
    <p v-if="error" class="text-red-600">{{ error }}</p>
    <p v-else class="text-slate-500">Signing in…</p>
  </div>
</template>
