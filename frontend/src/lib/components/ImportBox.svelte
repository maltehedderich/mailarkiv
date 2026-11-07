<script lang="ts">
  import FileUpIcon from "~icons/lucide/file-up";

  import FileName from "#lib/components/FileName.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import type { BrowserRefusal } from "#lib/import/choose.js";
  import { isBusy, type ImportStep } from "#lib/import/step.js";

  let {
    step,
    onchoose,
    onimport,
  }: {
    step: ImportStep;
    /** Every file chosen or dropped at once. The page decides what to keep. */
    onchoose: (files: File[]) => void;
    onimport: () => void;
  } = $props();

  // FR-034: shown until Slice 6 ships delete.
  const WARNING = "Imported mail can't be removed in this version.";

  // FR-036's messages. Each says that nothing left the browser.
  const REFUSALS: Record<BrowserRefusal, string> = {
    several: "Choose one file at a time. Nothing was uploaded.",
    empty: "This file is empty. Nothing was uploaded.",
    too_large: "This file is over the 150 MB limit. Nothing was uploaded.",
  };

  let input: HTMLInputElement;
  let box: HTMLElement;
  let dragging = $state(false);

  const busy = $derived(isBusy(step));
  const percent = $derived(
    step.kind === "uploading" ? Math.floor(step.fraction * 100) : 0,
  );

  /** Moves focus to Choose a file, such as when Import goes away. */
  export function focus() {
    input.focus();
  }

  function chosen() {
    const files = Array.from(input.files ?? []);
    // Cleared, so choosing the same file again still reports a change.
    input.value = "";
    if (!busy) onchoose(files);
  }

  function carriesFiles(event: DragEvent): boolean {
    return event.dataTransfer?.types.includes("Files") ?? false;
  }

  // FR-041: dropping adds to the Choose a file button and never replaces it.
  function dragOver(event: DragEvent) {
    if (!carriesFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect = busy ? "none" : "copy";
    }
    dragging = !busy;
  }

  function dragLeave(event: DragEvent) {
    if (!box.contains(event.relatedTarget as Node | null)) dragging = false;
  }

  function drop(event: DragEvent) {
    event.preventDefault();
    dragging = false;
    if (busy) return;
    onchoose(Array.from(event.dataTransfer?.files ?? []));
  }

  // A file dropped beside the box would otherwise replace the page.
  function keepPage(event: DragEvent) {
    if (carriesFiles(event)) event.preventDefault();
  }

  function importNow() {
    // Off, not disabled, while busy, so a focused button keeps its focus.
    if (!busy) onimport();
  }
</script>

<svelte:window ondragover={keepPage} ondrop={keepPage} />

<section
  bind:this={box}
  class="box"
  class:dragging
  aria-label="Import"
  ondragenter={dragOver}
  ondragover={dragOver}
  ondragleave={dragLeave}
  ondrop={drop}
>
  <FileUpIcon />

  <div class="text">
    {#if step.kind === "ready" || step.kind === "refused"}
      <p class="lead">Drop one .eml file here</p>
      <p class="note">Up to 150 MB. {WARNING}</p>
    {:else if step.kind === "chosen"}
      <p class="lead"><FileName name={step.file.name} /></p>
      <p class="note">{formatBytes(step.file.size)}. {WARNING}</p>
    {:else if step.kind === "uploading"}
      <p class="lead"><FileName name={step.file.name} /></p>
      <p class="note">Uploading, {percent}%</p>
      <div
        class="bar"
        role="progressbar"
        aria-label="Upload"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={percent}
      >
        <div class="sent" style:width="{percent}%"></div>
      </div>
    {:else}
      <p class="lead"><FileName name={step.file.name} /></p>
      <p class="note">Storing the file</p>
    {/if}
    <!-- Always present, so a screen reader hears a refusal when it appears. Not
         role="status": the import result is the page's one status element. -->
    <p class="problem" aria-live="polite" aria-atomic="true">
      {step.kind === "refused" ? REFUSALS[step.reason] : ""}
    </p>
  </div>

  <div class="actions">
    <label class="button choose" class:off={busy}>
      Choose a file
      <input
        bind:this={input}
        class="visually-hidden"
        type="file"
        multiple
        accept=".eml,message/rfc822"
        disabled={busy}
        onchange={chosen}
      />
    </label>
    {#if step.kind !== "ready" && step.kind !== "refused"}
      <button
        class="button primary"
        type="button"
        aria-disabled={busy ? "true" : undefined}
        onclick={importNow}
      >
        Import
      </button>
    {/if}
  </div>
</section>

<style>
  .box {
    display: flex;
    align-items: center;
    gap: var(--space-12);
    min-height: 76px;
    padding: var(--space-16);
    border: 1px solid var(--line);
    border-radius: var(--radius-large);
    background: var(--fill-1);
  }

  /* An inset edge, so the 1.5 px line doesn't move the content. */
  .box.dragging {
    border-color: transparent;
    background: var(--accent-tint);
    box-shadow: inset 0 0 0 1.5px var(--focus);
  }

  .box > :global(.icon) {
    color: var(--text-3);
  }

  .text {
    display: grid;
    flex: 1;
    gap: var(--space-2);
    min-width: 0;
  }

  .lead {
    display: flex;
    min-width: 0;
    font-weight: var(--weight-medium);
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  /* Never display: none, or a screen reader may miss the refusal arriving. */
  .problem {
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .bar {
    height: 4px;
    margin-top: var(--space-4);
    overflow: hidden;
    border-radius: 2px;
    background: var(--fill-3);
  }

  .sent {
    height: 100%;
    background: var(--text-1);
  }

  .actions {
    display: flex;
    flex: none;
    gap: var(--space-8);
  }

  /* The input is hidden inside its label, which looks like a button. */
  .choose {
    position: relative;
  }

  .choose:has(input:focus-visible) {
    outline: 2px solid var(--focus);
    outline-offset: 2px;
  }

  .choose.off {
    opacity: 0.45;
    pointer-events: none;
  }

  /* DESIGN.md: the buttons move below the text and fill the width, and the
     file icon goes. */
  @media (max-width: 720px) {
    .box {
      flex-wrap: wrap;
    }

    .box > :global(.icon) {
      display: none;
    }

    .text {
      flex-basis: 100%;
    }

    .actions {
      flex: 1;
    }

    .actions > * {
      flex: 1;
    }
  }
</style>
