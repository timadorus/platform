<script setup lang="ts">
import { computed, onUnmounted, provide, watch } from 'vue'
import { useRoute } from 'vue-router'
import { useChangeFeed } from '@/composables/useChangeFeed'

const route = useRoute()
// computed, not a plain const: App.vue never unmounts, so this must stay reactive across
// every navigation for the app's entire lifetime, not just param-only ones — the app-root
// equivalent of every view's own identical comment about vue-router instance reuse.
const universeId = computed(() => (route.params.universeId as string | undefined) ?? '')

const { lastChange: lastAggregateChange, start: startChangeFeed, stop: stopChangeFeed } = useChangeFeed()
provide('lastAggregateChange', lastAggregateChange)

watch(
  universeId,
  (id) => {
    // An empty id (e.g. the bare "/" Universe-picker route) means there's no single Universe
    // in scope for the catch-up poll — per design spec Decision 6, the live SSE connection
    // itself stays open regardless (a bare {type:'universe'} clause from UniversePickerView
    // still needs it), so there is deliberately no "stop the feed" branch here.
    if (id) startChangeFeed(id)
  },
  { immediate: true },
)

onUnmounted(stopChangeFeed)
</script>

<template>
  <router-view />
</template>
