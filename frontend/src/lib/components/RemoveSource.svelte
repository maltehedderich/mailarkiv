<script lang="ts">
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { removeSource } from "#lib/api.js";
  import { displayText } from "#lib/format/untrusted.js";
  import { signInPath } from "#lib/session.js";

  import UntrustedText from "./UntrustedText.svelte";

  // Asks before removing a source, naming it. Cancel comes first and takes
  // focus, so Enter alone removes nothing.

  let { id, name }: { id: number; name: string } = $props();

  const dom = $props.id();
  let dialog: HTMLDialogElement;
  let opener: HTMLElement | null = null;
  let busy = $state(false);
  let failed = $state(false);

  export function open() {
    opener =
      document.activeElement instanceof HTMLElement
        ? document.activeElement
        : null;
    failed = false;
    dialog.showModal();
  }

  function closed() {
    if (opener?.isConnected) opener.focus();
    opener = null;
  }

  // Escape would leave a removal running with nothing on screen to show it.
  function cancelled(event: Event) {
    if (busy) event.preventDefault();
  }

  async function remove() {
    if (busy) return;
    busy = true;
    failed = false;
    const result = await removeSource(String(id));
    switch (result.kind) {
      case "ok":
        opener = null;
        await goto("/sources");
        return;
      case "session_ended":
        opener = null;
        await goto(signInPath(page.url));
        return;
      case "unavailable":
        busy = false;
        failed = true;
    }
  }
</script>

<dialog
  bind:this={dialog}
  aria-label="Remove {displayText(name).text}?"
  aria-describedby="{dom}-text"
  onclose={closed}
  oncancel={cancelled}
>
  <h2>Remove <UntrustedText value={name} />?</h2>
  <p id="{dom}-text">
    Collection from this mailbox stops, and its stored password is deleted. The
    messages it collected stay in the archive, and their history still names
    this source.
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
      onclick={remove}
    >
      {busy ? "Removing…" : "Remove source"}
    </button>
  </div>
  <p class="problem" role="status">
    {failed
      ? "Remove didn't finish, so the source is still here. Try again. If this repeats, check the server log."
      : ""}
  </p>
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
    display: flex;
    min-width: 0;
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
    white-space: pre;
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
</style>
