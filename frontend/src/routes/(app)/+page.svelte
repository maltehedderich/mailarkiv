<script lang="ts">
  import { goto } from "$app/navigation";
  import { page } from "$app/state";

  import { getSession } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import MessageRow from "#lib/components/MessageRow.svelte";
  import { formatBytes, formatSize } from "#lib/format/size.js";
  import type { ImportResult } from "#lib/import/classify.js";
  import { upload } from "#lib/import/upload.js";
  import { signInPath } from "#lib/session.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  // A thin import form. T115 to T117 replace it with the import box.
  let input: HTMLInputElement;
  let busy = $state(false);
  let progress = $state("");
  let result: ImportResult | null = $state(null);

  const LIMIT = 150_000_000;

  // FR-038's meaning and next step for each failure.
  const FAILED = {
    storage_full:
      "The disk holding the archive ran out of space. Free disk space, then import the file again.",
    storage_write_failed:
      "The server could not write to its storage location. Fix the storage location, then import the file again.",
    catalog_write_failed:
      "The original was stored but its catalog entry was not written. Import the same file again. If this repeats, check the server log.",
  } as const;

  const REFUSED = {
    empty: "The file is empty.",
    too_large: `The file is larger than ${formatBytes(LIMIT)}.`,
    no_mail_header: "The file has no mail header line.",
  } as const;

  async function submit(event: SubmitEvent) {
    event.preventDefault();
    const file = input.files?.[0];
    if (busy || file === undefined) return;
    busy = true;
    // FR-040: a session that ended opens sign-in before any byte is sent.
    // Only a marked 401 changes anything. After any other result the upload
    // goes ahead, and its own answer decides the outcome.
    const session = await getSession();
    if (session.kind === "session_ended") {
      busy = false;
      await goto(signInPath(page.url));
      return;
    }
    // FR-032: the last result stays until the next import starts.
    result = null;
    progress = "Uploading, 0%";
    const outcome = await upload(file, {
      onProgress: (sent) => {
        progress = `Uploading, ${Math.floor(sent * 100)}%`;
      },
      onUploaded: () => {
        progress = "Storing";
      },
    });
    busy = false;
    progress = "";
    result = outcome;
    await goto("/", { refreshAll: true, reset: false });
  }
</script>

<svelte:head>
  <title>Messages · MailArkiv</title>
</svelte:head>

<main>
  <header>
    <h1>Messages</h1>
  </header>

  <div class="body">
    <form class="import" onsubmit={submit}>
      <label class="choose">
        <span>Choose a file</span>
        <input type="file" accept=".eml,message/rfc822" bind:this={input} />
      </label>
      <button
        class="button primary"
        type="submit"
        aria-disabled={busy ? "true" : undefined}
      >
        Import
      </button>
      <p class="progress">{progress}</p>
    </form>

    <div class="outcome" role="status">
      {#if result === null}
        <!-- Nothing yet. The element stays, so screen readers announce the
             result when it appears. -->
      {:else if result.outcome === "archived"}
        <p>
          <strong>Archived</strong>
          <bdi>{result.message.fileName ?? "(no file name)"}</bdi>,
          {formatSize(result.message.size)}.
        </p>
      {:else if result.outcome === "already_archived"}
        <p>
          <strong>Already archived</strong>
          This import was added to the history, and no second copy was stored.
        </p>
      {:else if result.outcome === "restored"}
        <p>
          <strong>Restored</strong>
          The stored copy was restored from this file.
        </p>
      {:else if result.outcome === "refused"}
        <p>
          <strong>Refused</strong>
          {REFUSED[result.reason]} Nothing was stored.
        </p>
      {:else if result.outcome === "failed"}
        <p>
          <strong>Failed</strong>
          The file is not archived. {FAILED[result.reason]}
        </p>
      {:else}
        <p>
          <strong>Unconfirmed</strong>
          The file may be archived. Importing it again is safe and stores no second
          copy.
        </p>
      {/if}
    </div>

    {#if data.messages.kind === "ok"}
      {@const { items, page, total } = data.messages.value}
      {#if total === 0}
        <p class="empty">No mail yet</p>
      {:else if items.length === 0}
        <p class="empty">Page {page} is past the end of the list.</p>
      {:else}
        <ul aria-label="Messages">
          {#each items as message (message.id)}
            <MessageRow {message} />
          {/each}
        </ul>
      {/if}
    {:else}
      <FailedLoad />
    {/if}
  </div>
</main>

<style>
  header {
    display: flex;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-24);
    border-bottom: 1px solid var(--line);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .body {
    display: grid;
    gap: var(--space-16);
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  .import {
    display: flex;
    flex-wrap: wrap;
    align-items: end;
    gap: var(--space-12);
    padding: var(--space-16);
    border: 1px solid var(--line);
    border-radius: var(--radius-large);
    background: var(--fill-1);
  }

  .choose {
    display: grid;
    gap: var(--space-6);
    min-width: 0;
  }

  .choose > span {
    color: var(--text-2);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  .choose > input {
    max-width: 100%;
  }

  .progress {
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
  }

  .outcome strong {
    font-weight: var(--weight-strong);
  }

  .empty {
    color: var(--text-3);
  }

  ul {
    display: grid;
    grid-template-columns: minmax(0, 1fr) max-content 62px 72px 40px var(
        --control-height
      );
    column-gap: var(--space-12);
    margin: 0;
    padding: 0;
    list-style: none;
  }
</style>
