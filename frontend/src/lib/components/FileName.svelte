<script lang="ts">
  import { displayName } from "#lib/format/file-name.js";

  import UntrustedText from "./UntrustedText.svelte";

  let {
    name,
    full = false,
  }: {
    name: string | null;
    /** Wrap the whole name instead of cutting its middle, as in the history. */
    full?: boolean;
  } = $props();

  const display = $derived(displayName(name));
</script>

{#if display.kind === "missing"}<span class="missing">{display.text}</span
  >{:else if name !== null}<UntrustedText value={name} {full} />{/if}

<style>
  .missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }
</style>
