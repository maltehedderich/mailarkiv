<script lang="ts">
  import { page as current } from "$app/state";

  let {
    page,
    pageSize,
    total,
  }: { page: number; pageSize: number; total: number } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const first = $derived((page - 1) * pageSize + 1);
  const last = $derived(Math.min(page * pageSize, total));
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

<!-- FR-019: every message can be reached, a page at a time. -->
<nav class="pager" aria-label="Pages">
  <p class="range">
    {numbers.format(first)}–{numbers.format(last)} of {numbers.format(total)}
  </p>
  {#if page > 1}
    <a class="button" href={pageHref(page - 1)}>Previous</a>
  {:else}
    <span class="button" role="link" aria-disabled="true">Previous</span>
  {/if}
  {#if page < lastPage}
    <a class="button" href={pageHref(page + 1)}>Next</a>
  {:else}
    <span class="button" role="link" aria-disabled="true">Next</span>
  {/if}
</nav>

<style>
  .pager {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    padding: 0 var(--space-4) 0 var(--space-10);
  }

  .range {
    margin-right: auto;
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }
</style>
