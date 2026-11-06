<script lang="ts">
  import { refreshAll } from "$app/navigation";

  // FR-045: data that didn't arrive leaves this block in place of the content,
  // never an empty list.

  let busy = false;

  async function retry() {
    // Not disabled while busy, so the focused button keeps focus.
    if (busy) return;
    busy = true;
    try {
      await refreshAll();
    } finally {
      busy = false;
    }
  }
</script>

<div class="failed" role="status">
  <p class="what">This page couldn't be loaded.</p>
  <p class="next">Try again. If this repeats, check the server log.</p>
  <button class="button" type="button" onclick={retry}>Try again</button>
</div>

<style>
  .failed {
    display: grid;
    justify-items: start;
    gap: var(--space-4);
    padding: var(--space-24) 0;
  }

  .what {
    color: var(--text-2);
  }

  .next {
    margin-bottom: var(--space-8);
    color: var(--text-3);
  }
</style>
