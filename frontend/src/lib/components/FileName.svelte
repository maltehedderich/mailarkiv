<script lang="ts">
  import { displayName, type NamePart } from "#lib/format/file-name.js";

  // A supplied name is plain text, always. This component never uses {@html},
  // and it adds no whitespace between parts, so the visible text is the name
  // with its codes in place.

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

{#snippet parts(
  list: NamePart[],
)}{#each list as part, index (index)}{#if part.kind === "code"}<span
        class="code">{part.text}</span
      >{:else}{part.text}{/if}{/each}{/snippet}

{#if display.kind === "missing"}<span class="missing">{display.text}</span
  >{:else if full}<bdi class="full"
    >{@render parts(display.start)}{@render parts(display.end)}</bdi
  >{:else}<bdi class="cut"
    ><span class="start">{@render parts(display.start)}</span><span class="end"
      ><span>{@render parts(display.end)}</span></span
    ></bdi
  >{/if}

<style>
  .missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  /* The start loses characters from its end, and the ending always shows, so
     the extension stays visible. bdi's dir="auto" orders the halves for a
     right-to-left name. Spaces show as typed: a flex item drops the space it
     ends with, so with nowrap "zum 1. Dezember.eml" read "zum 1.Dezember.eml"
     when the cut fell after the space. */
  .cut {
    display: inline-flex;
    max-width: 100%;
    min-width: 0;
    vertical-align: bottom;
    white-space: pre;
  }

  .start {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  /* The ending gives way only once the start is gone, and then loses its
     front, so the extension is the last thing to go. A column narrower than
     the ending showed "ement-29." for "statement-29.eml". */
  .end {
    display: flex;
    flex: none;
    justify-content: flex-end;
    max-width: 100%;
    overflow: hidden;
  }

  /* Spaces show as typed here too, so the history and the row agree on a
     name with two spaces in a row. */
  .full {
    overflow-wrap: anywhere;
    white-space: pre-wrap;
  }

  /* A neutralized character, such as U+202E. The label leading keeps the
     mono font's line box inside the line around it, so a name with a code is
     as tall as one without. */
  .code {
    line-height: var(--leading-label);
    margin: 0 1px;
    padding: 0 var(--space-2);
    border-radius: var(--radius-small);
    background: var(--fill-2);
    color: var(--text-2);
    font-family: var(--font-mono);
    font-size: var(--size-label);
    font-variant-ligatures: none;
    font-weight: var(--weight-regular);
  }
</style>
