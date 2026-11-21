<script lang="ts">
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { startRebuild, type RebuildResult } from "#lib/api.js";
  import { formatBinarySize } from "#lib/format/size.js";
  import { signInPath } from "#lib/session.js";

  // Asks before starting a rebuild. Cancel comes first and takes focus, so
  // Enter alone starts nothing.

  let { onstarted }: { onstarted: () => Promise<void> } = $props();

  const dom = $props.id();
  let dialog: HTMLDialogElement;
  let opener: HTMLElement | null = null;
  let busy = $state(false);
  let problem = $state("");

  export function open() {
    opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    problem = "";
    dialog.showModal();
  }

  function closed() {
    if (opener?.isConnected) opener.focus();
    opener = null;
  }

  // Escape would hide a start still waiting for its answer.
  function cancelled(event: Event) {
    if (busy) event.preventDefault();
  }

  function refusal(result: RebuildResult): string {
    switch (result.kind) {
      case "not_enough_space":
        return `A rebuild needs ${formatBinarySize(result.neededBytes)} free where the index lives, and ${formatBinarySize(result.freeBytes)} is free. Free disk space, then try again.`;
      case "rebuild_running":
        return "A rebuild is already running.";
      case "no_index":
        return "The first index is still being built. Start a rebuild once it's done.";
      case "search_unavailable":
        return "Search is unavailable, so a rebuild can't start. Try again once it's running.";
      default:
        return "The rebuild didn't start. Try again. If this repeats, check the server log.";
    }
  }

  async function start() {
    if (busy) return;
    busy = true;
    problem = "";
    const result = await startRebuild();
    switch (result.kind) {
      case "ok":
        // The button that opened this goes away once the rebuild shows, so
        // the caller moves focus instead.
        opener = null;
        dialog.close();
        busy = false;
        await onstarted();
        return;
      case "session_ended":
        opener = null;
        await goto(signInPath(page.url));
        return;
      default:
        busy = false;
        problem = refusal(result);
    }
  }
</script>

<dialog
  bind:this={dialog}
  aria-label="Rebuild the search index?"
  aria-describedby="{dom}-text"
  onclose={closed}
  oncancel={cancelled}
>
  <h2>Rebuild the search index?</h2>
  <p id="{dom}-text">
    MailArkiv builds a new index from every message in the archive. Search
    answers from the current index until the new one is complete, then switches
    to it. Both need room on disk until then.
  </p>
  <div class="buttons">
    <button
      class="button large"
      type="button"
      aria-disabled={busy ? "true" : undefined}
      onclick={() => busy || dialog.close()}
    >
      Cancel
    </button>
    <button
      class="button large primary"
      type="button"
      aria-disabled={busy ? "true" : undefined}
      onclick={start}
    >
      {busy ? "Starting…" : "Rebuild index"}
    </button>
  </div>
  <p class="problem" role="status">{problem}</p>
</dialog>

<style>
  /* A transparent edge, so forced colours still draw the box. */
  dialog {
    width: min(var(--dialog-width), calc(100% - 2 * var(--space-16)));
    max-width: none;
    padding: var(--space-20);
    border: 1px solid transparent;
    border-radius: var(--radius-large);
    background: var(--raised);
    box-shadow: var(--shadow-pop);
    color: var(--text-1);
  }

  dialog[open] {
    display: grid;
    gap: var(--space-12);
  }

  dialog::backdrop {
    background: var(--scrim);
  }

  h2 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  p {
    color: var(--text-2);
  }

  .buttons {
    display: flex;
    flex-wrap: wrap;
    justify-content: flex-end;
    gap: var(--space-8);
    margin-top: var(--space-4);
  }

  .problem {
    color: var(--state-bad-text);
  }

  /* The status stays in the page so a refusal is announced. Empty, it still
     takes a gap, which the padding gives back so the dialog's edges are even. */
  dialog:has(.problem:empty) {
    padding-bottom: calc(var(--space-20) - var(--space-12));
  }
</style>
