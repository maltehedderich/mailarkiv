<script lang="ts">
  import type { SearchField, SearchPage, SearchResult } from "#lib/api.js";
  import { formatTimeToday } from "#lib/format/time.js";
  import { CATEGORIES } from "#lib/status/categories.js";

  // What a search found, or why it found nothing. One status region, so a
  // new answer is read out whole.

  let {
    result,
    shown,
    maxResults,
  }: {
    /** The answer for the page asked for, or null while it loads. */
    result: SearchResult | null;
    /** The last page this search answered, kept while another loads. */
    shown: SearchPage | null;
    /** How many results the pages can reach. */
    maxResults: number;
  } = $props();

  const counts = new Intl.NumberFormat("en-US");

  function plural(count: number, one: string, many: string): string {
    return `${counts.format(count)} ${count === 1 ? one : many}`;
  }

  const LABELS: Record<SearchField, string> = {
    words: "Words",
    from: "From",
    to: "To",
    subject: "Subject",
    attachment: "Attachment name",
    list_date_from: "First day",
    list_date_before: "Last day",
    has_attachments: "Has attachments",
    page: "Page",
  };

  /** The failure to show instead of the results, if any. */
  const problem = $derived.by(() => {
    if (result === null || result.kind === "ok") return null;
    switch (result.kind) {
      case "search_took_too_long":
        return "This search took too long. Add words or filters.";
      case "search_unavailable": {
        const next =
          result.nextStartAt === null
            ? "A start is under way."
            : `Next start ${formatTimeToday(result.nextStartAt, new Date())}.`;
        return `Search is unavailable: ${CATEGORIES[result.cause].name}. ${next} The list and downloads still work.`;
      }
      case "search_did_not_answer":
        return "Search didn't answer. Try again.";
      case "invalid_fields":
        return `Search couldn't use ${result.fields.map((field) => LABELS[field]).join(", ")}. Change ${result.fields.length === 1 ? "it" : "them"} and search again.`;
      case "page_too_deep":
        return `Search shows at most the first ${counts.format(maxResults)} results. Add words or filters.`;
      default:
        return "Search didn't finish. Try again.";
    }
  });
</script>

<div class="results-header" role="status">
  {#if problem !== null}
    <p class="problem">{problem}</p>
  {:else if shown !== null}
    {#if shown.fullList}
      <p class="total">
        These words have nothing to search for, so this is the whole list.
      </p>
    {:else}
      <p class="total">
        {shown.total === 0
          ? "No messages match."
          : `${plural(shown.total, "message matches", "messages match")}.`}
      </p>
      {#if shown.notIncluded > 0}
        <p class="note">
          {plural(shown.notIncluded, "message isn't", "messages aren't")} included
          yet, because processing or indexing hasn't reached them.
        </p>
      {/if}
      {#if shown.total > maxResults}
        <p class="note">
          Only the first {counts.format(maxResults)} can be shown. Add words or filters
          to narrow the search.
        </p>
      {/if}
    {/if}
  {/if}
</div>

<style>
  /* Holds a line while the first answer loads, so the rows don't move when
     it arrives. */
  .results-header {
    display: grid;
    gap: var(--space-2);
    min-height: var(--leading-body);
  }

  .total {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .note {
    color: var(--text-3);
  }

  .problem {
    color: var(--state-bad-text);
  }
</style>
