<script lang="ts">
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import type { Attachment } from "#lib/api.js";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { cleanFileName, fetchAttachment } from "#lib/download/attachment.js";
  import { saveFile } from "#lib/download/save.js";
  import { formatSize } from "#lib/format/size.js";
  import { KIND_NOTES } from "#lib/message/words.js";
  import { signInPath } from "#lib/session.js";
  import { failureWords } from "#lib/toasts/words.js";

  // Every part other than the displayed bodies, with a Save for each. Save
  // fetches the decoded bytes and saves them only once all have arrived,
  // under the cleaned name, never shown in the browser.

  let {
    messageId,
    attachments,
  }: { messageId: number; attachments: Attachment[] } = $props();

  type Saving =
    | { kind: "saving"; percent: number }
    | { kind: "failed"; what: string; next: string };

  /** Saves by part number. A save that succeeds leaves no line. */
  let saving = $state<Record<string, Saving>>({});

  async function save(attachment: Attachment, name: string) {
    const { position } = attachment;
    if (saving[position]?.kind === "saving") return;
    saving[position] = { kind: "saving", percent: 0 };
    const result = await fetchAttachment(messageId, position, (fraction) => {
      saving[position] = {
        kind: "saving",
        percent: Math.floor(fraction * 100),
      };
    });
    switch (result.kind) {
      case "ok":
        delete saving[position];
        saveFile(result.file, name);
        return;
      case "session_ended":
        delete saving[position];
        await goto(signInPath(page.url));
        return;
      case "canceled":
        delete saving[position];
        return;
      case "failed":
        saving[position] = {
          kind: "failed",
          ...failureWords(result.reason, "This attachment wasn't saved."),
        };
    }
  }
</script>

<section class="attachments" aria-labelledby="attachments-heading">
  <h2 id="attachments-heading">
    Attachments <span class="count"
      >{attachments.length.toLocaleString("en")}</span
    >
  </h2>
  {#if attachments.length === 0}
    <p class="none">No attachments.</p>
  {:else}
    <ul>
      {#each attachments as attachment (attachment.position)}
        {@const name = cleanFileName(
          attachment.name,
          attachment.position,
          attachment.kind,
        )}
        {@const state = saving[attachment.position]}
        {@const note = KIND_NOTES[attachment.kind]}
        <li>
          <span class="name"
            >{#if attachment.name === null}<span class="missing">(no name)</span
              >{:else}<UntrustedText value={attachment.name} />{/if}</span
          >
          <span class="type"
            >{#if attachment.declaredType === null}<span class="missing"
                >No type</span
              >{:else}<UntrustedText
                value={attachment.declaredType}
              />{/if}</span
          >
          <span class="size">{formatSize(attachment.size)}</span>
          <button
            class="button save"
            type="button"
            aria-label="Save {name}"
            aria-disabled={state?.kind === "saving" ? "true" : undefined}
            onclick={() => save(attachment, name)}>Save</button
          >
          {#if note !== null || attachment.damaged}
            <span class="notes"
              >{#if note !== null}<span>{note}</span
                >{/if}{#if attachment.damaged}<span class="damaged"
                  ><span class="dot"></span>Damaged encoding. Save keeps the
                  bytes that could be decoded.</span
                >{/if}</span
            >
          {/if}
          <span class="saving" role="status"
            >{#if state?.kind === "saving"}Saving… {state.percent}%{:else if state?.kind === "failed"}<span
                class="what">{state.what}</span
              > <span class="next">{state.next}</span>{/if}</span
          >
        </li>
      {/each}
    </ul>
  {/if}
</section>

<style>
  h2 {
    margin: 0 0 var(--space-8);
    color: var(--text-1);
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .count {
    color: var(--text-3);
    font-weight: var(--weight-regular);
    font-variant-numeric: tabular-nums;
  }

  .none,
  .missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  ul {
    display: grid;
    gap: var(--space-4);
    margin: 0 calc(-1 * var(--space-6)) 0 calc(-1 * var(--space-10));
    padding: 0;
    list-style: none;
  }

  /* Name, type, and size share the width, and Save ends the row. Notes and
     a save's progress go on lines below, under the name. */
  li {
    display: grid;
    grid-template-columns: minmax(0, 2fr) minmax(0, 1fr) 62px auto;
    column-gap: var(--space-16);
    align-items: center;
    min-height: var(--row-height);
    padding: var(--space-4) var(--space-6) var(--space-4) var(--space-10);
    border-radius: var(--radius-medium);
  }

  li:hover {
    background: var(--fill-1);
  }

  .name {
    display: flex;
    min-width: 0;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .type {
    display: flex;
    min-width: 0;
    color: var(--text-2);
  }

  .size {
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
    text-align: right;
  }

  .notes,
  .saving {
    grid-column: 1 / -1;
    color: var(--text-3);
    font-size: var(--size-label);
    line-height: var(--leading-label);
  }

  .notes {
    display: flex;
    flex-wrap: wrap;
    gap: var(--space-4) var(--space-12);
  }

  .saving:empty {
    display: none;
  }

  .saving {
    font-variant-numeric: tabular-nums;
  }

  .saving .what {
    color: var(--state-bad-text);
  }

  .damaged {
    display: inline-flex;
    gap: var(--space-6);
    align-items: baseline;
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--state-warn);
    transform: translateY(-1px);
  }

  /* DESIGN.md: at 720 px and below the name takes the width, then the type,
     size, and notes, with Save beside them. */
  @media (max-width: 720px) {
    ul {
      margin-left: calc(-1 * var(--space-8));
    }

    li {
      grid-template-columns: minmax(0, 1fr) auto;
      row-gap: var(--space-2);
      padding-left: var(--space-8);
    }

    .name {
      grid-column: 1 / -1;
    }

    .size {
      text-align: left;
    }

    .save {
      grid-row: 2 / span 2;
      grid-column: 2;
    }
  }
</style>
