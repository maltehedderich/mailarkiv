<script lang="ts">
  import { REMOTE_IMAGES } from "#lib/message/view.js";

  // Says the HTML refers to images on the internet that aren't loaded, what
  // loading them can tell the sender, and offers to load them for this view.

  let { onload }: { onload: () => void } = $props();
</script>

<div class="notice">
  <p class="what">{REMOTE_IMAGES.what}</p>
  <p class="why">{REMOTE_IMAGES.why}</p>
  <button class="button" type="button" onclick={onload}
    >{REMOTE_IMAGES.action}</button
  >
</div>

<style>
  .notice {
    display: grid;
    justify-items: start;
    gap: var(--space-4);
    padding: var(--space-12);
    border: 1px solid transparent;
    border-radius: var(--radius-medium);
    background: var(--fill-1);
  }

  .what {
    color: var(--text-2);
  }

  .why {
    color: var(--text-3);
  }

  .button {
    margin-top: var(--space-8);
  }
</style>
