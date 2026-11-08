<script lang="ts">
  import NextIcon from "~icons/lucide/chevron-right";
  import PreviousIcon from "~icons/lucide/chevron-left";

  import { page as current } from "$app/state";

  let {
    page,
    pageSize,
    total,
  }: { page: number; pageSize: number; total: number } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const first = $derived(numbers.format((page - 1) * pageSize + 1));
  const last = $derived(numbers.format(Math.min(page * pageSize, total)));
  const all = $derived(numbers.format(total));
  const lastPage = $derived(Math.max(1, Math.ceil(total / pageSize)));

  /** This address with another page, keeping any other parameter. */
  function pageHref(number: number): string {
    const url = new URL(current.url.href);
    url.searchParams.delete("message");
    if (number === 1) url.searchParams.delete("page");
    else url.searchParams.set("page", String(number));
    return url.pathname + url.search;
  }
</script>

<!-- FR-019: every message can be reached, a page at a time. An end keeps its
     arrow, off, so the other one never moves. -->
<nav class="pager" aria-label="Pages">
  <!-- Screen readers may skip the dash, which would leave "1 50 of 7,000".
       The no-break space keeps "of" with the total when the range wraps. -->
  <p class="range">
    <span aria-hidden="true">{first}–{last} of&nbsp;{all}</span>
    <span class="visually-hidden">Messages {first} to {last} of {all}</span>
  </p>
  <div class="arrows">
    {#if page > 1}
      <a
        class="button ghost icon-only"
        href={pageHref(page - 1)}
        aria-label="Previous page"
      >
        <PreviousIcon />
      </a>
    {:else}
      <span
        class="button ghost icon-only"
        role="link"
        aria-disabled="true"
        aria-label="Previous page"
      >
        <PreviousIcon />
      </span>
    {/if}
    {#if page < lastPage}
      <a
        class="button ghost icon-only"
        href={pageHref(page + 1)}
        aria-label="Next page"
      >
        <NextIcon />
      </a>
    {:else}
      <span
        class="button ghost icon-only"
        role="link"
        aria-disabled="true"
        aria-label="Next page"
      >
        <NextIcon />
      </span>
    {/if}
  </div>
</nav>

<style>
  /* The last arrow's glyph ends on the header's padding, like the title's
     starts on it. The ghost button's edge is invisible. */
  .pager {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    margin-right: calc((var(--icon-size) - var(--control-height)) / 2);
  }

  .range {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
    text-align: right;
  }

  .arrows {
    display: flex;
    flex: none;
    gap: var(--space-2);
  }

  @media (max-width: 720px) {
    .pager {
      margin-right: calc((var(--icon-size) - var(--control-height-touch)) / 2);
    }
  }
</style>
