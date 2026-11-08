<script lang="ts">
  import { untrack } from "svelte";
  import FileUpIcon from "~icons/lucide/file-up";

  import FileName from "#lib/components/FileName.svelte";
  import { displayName } from "#lib/format/file-name.js";
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

  /**
   * A busy step is announced only once it has lasted this long, so a quick
   * import is heard as its result alone. Downloading waits as long.
   */
  const SETTLE_MS = 400;

  let box: HTMLElement;
  let input: HTMLInputElement;
  let importButton: HTMLButtonElement;

  const busy = $derived(isBusy(step));
  /** A file is chosen or on its way, so Import has something to send. */
  const holding = $derived(step.kind !== "ready" && step.kind !== "refused");
  const percent = $derived(
    step.kind === "uploading" ? Math.floor(step.fraction * 100) : 0,
  );

  // What a screen reader hears. The steps show on screen, but only a live
  // region is heard without moving focus (WCAG 4.1.3).
  let said = $state("");

  // A new choice or refusal is a new step object, even with the same words.
  // Progress isn't new: it moves the bar, which a screen reader can query.
  const moment = $derived(step.kind === "uploading" ? "uploading" : step);

  $effect(() => {
    void moment;
    const now = untrack(() => step);
    // Cleared first, so the same words are announced again after a second try.
    said = "";
    const words = announcement(now);
    if (words === undefined) return;
    const timer = setTimeout(
      () => (said = words),
      isBusy(now) ? SETTLE_MS : 100,
    );
    return () => clearTimeout(timer);
  });

  function announcement(now: ImportStep): string | undefined {
    switch (now.kind) {
      case "ready":
        // The result toast speaks for a finished import.
        return undefined;
      case "chosen": {
        const name = displayName(now.file.name).text;
        return `${name}, ${formatBytes(now.file.size)}. ${WARNING}`;
      }
      case "uploading":
        return "Uploading";
      case "storing":
        return "Storing the file";
      case "refused":
        return REFUSALS[now.reason];
    }
  }

  // Import turns off when the import ends or a refusal replaces the choice. If
  // it had focus, focus moves to Choose a file on purpose instead of falling to
  // the page. Focus anywhere else stays where the person left it.
  let importHadFocus = false;
  $effect.pre(() => {
    void step;
    importHadFocus =
      importButton !== undefined && document.activeElement === importButton;
  });
  $effect(() => {
    if (!holding && importHadFocus) input.focus();
  });

  function chosen() {
    const files = Array.from(input.files ?? []);
    // Cleared, so choosing the same file again still reports a change.
    input.value = "";
    if (!busy) onchoose(files);
  }

  function importNow() {
    // Off, not disabled, while busy, so a focused button keeps its focus.
    if (!busy) onimport();
  }

  // Each element a dragged file has entered and not yet left. Kept by target,
  // because Safari leaves relatedTarget empty on dragleave.
  let entered = $state.raw<Node[]>([]);
  /** A file is dragged somewhere over the page. */
  const dragging = $derived(!busy && entered.length > 0);
  /** A file is dragged over the box itself. */
  const over = $derived(dragging && entered.some((node) => box.contains(node)));

  function carriesFiles(event: DragEvent): boolean {
    return event.dataTransfer?.types.includes("Files") ?? false;
  }

  function dragEnter(event: DragEvent) {
    if (!carriesFiles(event)) return;
    const target = event.target as Node;
    entered = [...leave(target), target];
  }

  function dragLeave(event: DragEvent) {
    entered = leave(event.target as Node);
  }

  function leave(target: Node): Node[] {
    return entered.filter((node) => node !== target && node.isConnected);
  }

  // FR-041: dropping adds to the Choose a file button and never replaces it.
  // Cancelled everywhere, or a file dropped beside the box would replace the
  // page. Only the box takes the file, and elsewhere the cursor says so.
  function dragOver(event: DragEvent) {
    if (!carriesFiles(event)) return;
    event.preventDefault();
    if (event.dataTransfer) {
      event.dataTransfer.dropEffect =
        !busy && box.contains(event.target as Node) ? "copy" : "none";
    }
  }

  function drop(event: DragEvent) {
    if (!carriesFiles(event)) return;
    event.preventDefault();
    entered = [];
    if (busy || !box.contains(event.target as Node)) return;
    onchoose(Array.from(event.dataTransfer?.files ?? []));
  }
</script>

<svelte:window
  ondragenter={dragEnter}
  ondragleave={dragLeave}
  ondragover={dragOver}
  ondrop={drop}
/>

<!-- Two lines of text in every step, so the box keeps its height and the list
     under it doesn't move during an import. -->
<section
  bind:this={box}
  class="box"
  class:dragging
  class:over
  aria-label="Import"
>
  <FileUpIcon />

  <div class="text">
    {#if step.kind === "ready"}
      <p class="lead">
        <span class="pointer">Drop one .eml file here</span>
        <span class="touch">Import one .eml file</span>
      </p>
      <p class="note">Up to 150 MB. {WARNING}</p>
    {:else if step.kind === "refused"}
      <!-- Shown again on each refusal, so a second try visibly happened. -->
      {#key step}
        <p class="lead problem">{REFUSALS[step.reason]}</p>
      {/key}
      <p class="note">Up to 150 MB. {WARNING}</p>
    {:else}
      <p class="lead"><FileName name={step.file.name} /></p>
      {#if step.kind === "chosen"}
        <p class="note">{formatBytes(step.file.size)}. {WARNING}</p>
      {:else if step.kind === "uploading"}
        <div class="note progress">
          <span>Uploading</span>
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
          <!-- The bar gives screen readers the value, so this isn't read
               again at every step. -->
          <span class="percent" aria-hidden="true">{percent}%</span>
        </div>
      {:else}
        <p class="note">Storing the file</p>
      {/if}
    {/if}
  </div>

  <p class="visually-hidden" role="status">{said}</p>

  <div class="actions">
    <label class="button choose">
      Choose a file
      <!-- One file only, so the dialog itself prevents several. A drop can
           still bring several, and the page refuses them (FR-036). -->
      <input
        bind:this={input}
        class="visually-hidden"
        type="file"
        accept=".eml,message/rfc822"
        disabled={busy}
        onchange={chosen}
      />
    </label>
    <!-- Shown in every step, so choosing a file moves neither button and
         Import never lands under the pointer that pressed Choose a file. -->
    <button
      bind:this={importButton}
      class="button"
      class:primary={holding}
      type="button"
      disabled={!holding}
      aria-disabled={busy ? "true" : undefined}
      onclick={importNow}
    >
      Import
    </button>
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

  /* A file is dragged somewhere over the page, so the box shows where it
     goes. A border, so forced colours draw it too. */
  .box.dragging {
    border-color: var(--focus);
    border-style: dashed;
  }

  /* Over the box. An inset edge, so the 1.5 px line doesn't move the
     content. */
  .box.over {
    border-color: transparent;
    border-style: solid;
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

  /* A refusal can take two lines at 360 px. */
  .lead.problem {
    display: block;
    animation: arrive var(--motion-enter) var(--ease-enter);
  }

  @keyframes arrive {
    from {
      opacity: 0;
    }
  }

  /* Phones can't drag a file, so the lead names the task instead. */
  .touch {
    display: none;
  }

  @media (pointer: coarse) {
    .pointer {
      display: none;
    }

    .touch {
      display: inline;
    }
  }

  .note {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  /* The bar sits in the note's line, as in the download toast, so starting
     and finishing the upload doesn't change the box's height. */
  .progress {
    display: flex;
    align-items: center;
    gap: var(--space-8);
  }

  .bar {
    flex: 1;
    height: 4px;
    overflow: hidden;
    border-radius: 2px;
    background: var(--fill-3);
  }

  .sent {
    height: 100%;
    background: var(--text-1);
  }

  .percent {
    min-width: 4ch;
    text-align: right;
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

  /* DESIGN.md: the buttons move below the text and share the width, and the
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
