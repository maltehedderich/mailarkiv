<script lang="ts">
  const id = $props.id();
  let dialog: HTMLDialogElement;
  let keepButton: HTMLButtonElement;
  let proceed: (() => void) | undefined;

  export function open(discard: () => void) {
    if (dialog.open) return;
    proceed = discard;
    dialog.showModal();
    keepButton.focus();
  }

  function discard() {
    const next = proceed;
    proceed = undefined;
    dialog.close();
    next?.();
  }
</script>

<dialog
  bind:this={dialog}
  aria-labelledby="{id}-title"
  aria-describedby="{id}-text"
>
  <h2 id="{id}-title">Discard this source?</h2>
  <p id="{id}-text">
    The details you entered will be lost. Nothing has been saved.
  </p>
  <div class="buttons">
    <button
      bind:this={keepButton}
      class="button large"
      type="button"
      onclick={() => dialog.close()}
    >
      Keep editing
    </button>
    <button class="button large primary" type="button" onclick={discard}>
      Discard source
    </button>
  </div>
</dialog>

<style>
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
</style>
