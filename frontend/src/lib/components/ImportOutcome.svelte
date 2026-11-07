<script lang="ts" module>
  import type { ImportResult } from "#lib/import/classify.js";

  /** An import's result, with the name of the file that was sent. */
  export interface Shown {
    fileName: string;
    result: ImportResult;
  }
</script>

<script lang="ts">
  import FileName from "#lib/components/FileName.svelte";
  import { formatSize } from "#lib/format/size.js";
  import type { FailedReason, RefusedReason } from "#lib/import/classify.js";
  import { CATEGORIES } from "#lib/status/categories.js";

  let {
    shown,
    historyHref,
  }: {
    /** Null shows nothing, while an import runs or before the first one. */
    shown: Shown | null;
    historyHref: (id: number) => string;
  } = $props();

  type Outcome = ImportResult["outcome"];

  // FR-032's words, and DESIGN.md's dot for each outcome.
  const WORDS: Record<Outcome, { word: string; tone: string }> = {
    archived: { word: "Archived", tone: "ok" },
    already_archived: { word: "Already archived", tone: "neutral" },
    restored: { word: "Restored", tone: "warn" },
    refused: { word: "Refused", tone: "bad" },
    failed: { word: "Failed", tone: "bad" },
    unconfirmed: { word: "Unconfirmed", tone: "warn" },
  };

  const REFUSED: Record<RefusedReason, string> = {
    empty: "This file is empty.",
    too_large: "This file is over the 150 MB limit.",
    no_mail_header: "This file doesn't start with a mail header line.",
  };

  // FR-038's meaning and next step for a capture failure. FR-033 names the
  // proxy's limit, and a marked 403 is MailArkiv refusing the request.
  const FAILED: Record<FailedReason, { meaning: string; nextStep: string }> = {
    storage_full: CATEGORIES.storage_full,
    storage_write_failed: CATEGORIES.storage_write_failed,
    catalog_write_failed: CATEGORIES.catalog_write_failed,
    proxy_limit: {
      meaning: "The reverse proxy's upload limit refused it.",
      nextStep:
        "Ask the operator to let the reverse proxy accept uploads up to 150 MB.",
    },
    refused_by_server: {
      meaning: "MailArkiv refused the request.",
      nextStep: "Try again. If this repeats, check the server log.",
    },
  };

  const UNCONFIRMED =
    "The file may be archived, and importing it again is safe and stores no second copy.";
</script>

<!-- Always present, so screen readers announce each result as it appears
     (FR-041). It stays until the next import starts (FR-032). -->
<div class="outcome" role="status" aria-label="Import result">
  {#if shown !== null}
    {@const { result } = shown}
    {@const { word, tone } = WORDS[result.outcome]}
    <p class="state">
      <span class="dot {tone}"></span>
      <strong>{word}</strong>
      <span class="file">
        <FileName
          name={"message" in result ? result.message.fileName : shown.fileName}
        />
      </span>
      {#if result.outcome === "archived"}
        <span class="size">{formatSize(result.message.size)}</span>
      {/if}
    </p>

    {#if result.outcome === "already_archived"}
      <p class="what">
        This import was added to the history, and no second copy was stored.
      </p>
    {:else if result.outcome === "restored"}
      <p class="what">The stored copy was restored from this file.</p>
    {:else if result.outcome === "refused"}
      <p class="what">{REFUSED[result.reason]} Nothing was stored.</p>
    {:else if result.outcome === "failed"}
      {@const { meaning, nextStep } = FAILED[result.reason]}
      <p class="what">The file is not archived. {meaning}</p>
      <p class="next">{nextStep}</p>
    {:else if result.outcome === "unconfirmed"}
      <p class="what">{UNCONFIRMED}</p>
    {/if}

    {#if "message" in result}
      <!-- Keeping focus here lets the drawer give it back to this link. -->
      <a href={historyHref(result.message.id)} data-sveltekit-reset="false"
        >Open import history</a
      >
    {/if}
  {/if}
</div>

<style>
  .outcome {
    display: grid;
    justify-items: start;
    gap: var(--space-2);
    padding: 0 var(--space-4);
  }

  .state {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    max-width: 100%;
    min-width: 0;
  }

  .state strong {
    flex: none;
    font-weight: var(--weight-strong);
  }

  .file {
    display: flex;
    min-width: 0;
    color: var(--text-2);
    font-weight: var(--weight-medium);
  }

  .size {
    flex: none;
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
  }

  .dot.ok {
    background: var(--state-ok);
  }

  .dot.neutral {
    background: var(--text-4);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .dot.bad {
    background: var(--state-bad);
  }

  .what {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }
</style>
