<script lang="ts">
  import type { Observation } from "#lib/api.js";

  import FileName from "./FileName.svelte";
  import UntrustedText from "./UntrustedText.svelte";

  // Names a message by one of its entries on one line: an import by its file
  // name, a collection by its source name and folder. Each name is cut in its
  // own middle.

  let { entry }: { entry: Observation } = $props();
</script>

{#if entry.sourceType === "manual_import"}<FileName
    name={entry.fileName}
  />{:else}<span class="collection"
    ><span class="part"><UntrustedText value={entry.source.name} /></span><span
      class="part folder"><UntrustedText value={entry.folder} /></span
    ></span
  >{/if}

<style>
  /* Any character could appear in either name, so weight and colour, which
     the names can't fake, mark where the source name ends. */
  .collection {
    display: flex;
    flex: auto;
    gap: var(--space-6);
    min-width: 0;
  }

  .part {
    display: flex;
    min-width: 0;
  }

  /* The folder gives way first. Sharing the shrink cut "Anna" to "A…" beside
     a long folder. Half the room keeps a long source name from taking all,
     and the pair fills its column so half is of the column. Half of the pair
     cut "Büro" beside a short folder. */
  .part:first-child {
    flex: none;
    max-width: 50%;
  }

  .folder {
    color: var(--text-2);
    font-weight: var(--weight-regular);
  }
</style>
