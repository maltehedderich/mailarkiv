<script lang="ts">
  import type { Sender } from "#lib/api.js";
  import { senderDisplay } from "#lib/format/sender.js";

  import UntrustedText from "./UntrustedText.svelte";

  // A sender on one line: the name cut at its end, then the address cut
  // before its @. Each part is its own <bdi>, so a right-to-left name can't
  // reorder the address.

  let { sender }: { sender: Sender | null } = $props();

  const shown = $derived(senderDisplay(sender));
</script>

{#if shown.kind === "missing"}<span class="missing">{shown.text}</span
  >{:else}<span class="sender"
    >{#if shown.name !== null}<span class="part name"
        ><UntrustedText split={shown.name} /></span
      >{/if}{#if shown.address !== null}<span
        class="part address"
        class:alone={shown.name === null}
        ><UntrustedText split={shown.address} /></span
      >{/if}</span
  >{/if}

<style>
  /* Both parts grow alike until each fits, so a short part shows whole and
     two long ones share the line half and half. A long name then can't push
     the domain out of view. */
  .sender {
    display: grid;
    grid-auto-columns: minmax(0, max-content);
    grid-auto-flow: column;
    column-gap: var(--space-6);
    min-width: 0;
  }

  .part {
    display: flex;
    min-width: 0;
  }

  /* A name can hold "@" and "<", so weight and colour, which it can't fake,
     mark where it ends. */
  .address {
    color: var(--text-2);
    font-weight: var(--weight-regular);
  }

  .address.alone {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }
</style>
