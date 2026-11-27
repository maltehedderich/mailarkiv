<script lang="ts">
  // The text body as text: Svelte escapes it, and nothing turns an address
  // in it into a link.

  let { text }: { text: string } = $props();
</script>

<div class="plain" dir="auto">{text}</div>

<style>
  .plain {
    color: var(--text-1);
    white-space: pre-wrap;
    overflow-wrap: anywhere;
  }
</style>
