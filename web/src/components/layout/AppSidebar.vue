<script setup lang="ts"></script>

<template>
  <aside class="flex w-72 flex-shrink-0 flex-col gap-4 overflow-y-auto border-r border-slate-200 bg-white p-3">
    <section>
      <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-400">Characters</h2>
      <slot name="characters" />
    </section>
    <section>
      <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-400">Entities</h2>
      <slot name="entities" />
    </section>
    <section>
      <h2 class="mb-2 text-xs font-semibold uppercase tracking-wide text-slate-400">Objects</h2>
      <slot name="objects" />
    </section>
  </aside>
</template>
