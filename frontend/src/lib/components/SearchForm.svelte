<script lang="ts">
  import ChevronIcon from "~icons/lucide/chevron-down";

  import { filterCount, type SearchForm } from "#lib/search/form.js";

  import FormField from "./FormField.svelte";

  // The search form. The inputs have no name, so a submit before the page
  // has loaded its script sends nothing, and autocomplete is off, so the
  // browser keeps no list of what was typed.

  let {
    form = $bindable(),
    open = $bindable(false),
    dayProblem,
    canClear,
    onsearch,
    onclear,
  }: {
    form: SearchForm;
    /** Whether the filters show. */
    open?: boolean;
    /** Why the days can't be searched, shown under Last day. */
    dayProblem: string | null;
    canClear: boolean;
    onsearch: () => void;
    onclear: () => void;
  } = $props();

  const id = $props.id();
  const count = $derived(filterCount(form));

  function submit(event: SubmitEvent) {
    event.preventDefault();
    onsearch();
  }
</script>

<form
  class="search"
  role="search"
  autocomplete="off"
  novalidate
  onsubmit={submit}
>
  <div class="line">
    <label class="field words">
      <span>Words</span>
      <input type="search" bind:value={form.words} maxlength="1000" />
    </label>
    <div class="actions">
      <button class="button large" type="submit">Search</button>
      <button
        class="button large ghost"
        type="button"
        aria-expanded={open}
        aria-controls="{id}-filters"
        onclick={() => (open = !open)}
        >Filters{#if count > 0}<span class="count" aria-hidden="true"
            >{count}</span
          ><span class="visually-hidden">, {count} in use</span
          >{/if}<ChevronIcon class="chevron" aria-hidden="true" /></button
      >
      {#if canClear}
        <button class="button large ghost" type="button" onclick={onclear}
          >Clear</button
        >
      {/if}
    </div>
  </div>

  <div id="{id}-filters" class="filters" hidden={!open}>
    <FormField
      label="From"
      bind:value={form.from}
      maxlength={1000}
      hint="A name, an address, or a domain such as example.com"
    />
    <FormField
      label="To"
      bind:value={form.to}
      maxlength={1000}
      hint="Also matches Cc and Bcc"
    />
    <FormField label="Subject" bind:value={form.subject} maxlength={1000} />
    <FormField
      label="Attachment name"
      bind:value={form.attachment}
      maxlength={1000}
    />
    <div class="days">
      <FormField label="First day" type="date" bind:value={form.firstDay} />
      <FormField
        label="Last day"
        type="date"
        bind:value={form.lastDay}
        problem={dayProblem}
      />
    </div>
    <label class="check">
      <input type="checkbox" bind:checked={form.hasAttachments} />
      <span>Has attachments</span>
    </label>
  </div>
</form>

<style>
  .search {
    display: grid;
    gap: var(--space-16);
  }

  .line {
    display: flex;
    align-items: flex-end;
    gap: var(--space-8);
  }

  /* An input keeps its default width unless told otherwise, which pushed
     Clear off a 360 px screen. */
  .words {
    flex: auto;
    min-width: 0;
  }

  .words > input {
    min-width: 0;
  }

  .actions {
    display: flex;
    flex: none;
    gap: var(--space-8);
  }

  .count {
    color: var(--text-3);
    font-variant-numeric: tabular-nums;
  }

  /* Points down while the filters are hidden, up while they show. */
  .search :global(.chevron) {
    width: var(--icon-size-small);
    height: var(--icon-size-small);
    color: var(--text-3);
    transition: transform var(--motion-fade);
  }

  [aria-expanded="true"] > :global(.chevron) {
    transform: rotate(180deg);
  }

  /* Rows of two, so a field and its partner share a line, and the hints
     under From and To start level. */
  .filters {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
    gap: var(--space-16);
  }

  .filters[hidden] {
    display: none;
  }

  /* Top-aligned, so a problem under Last day doesn't move First day. */
  .days {
    display: grid;
    grid-template-columns: repeat(2, minmax(0, 1fr));
    align-items: start;
    gap: var(--space-8);
  }

  .days :global(input) {
    min-width: 0;
  }

  /* As tall as an input, and level with the days' inputs beside it. */
  .check {
    display: flex;
    align-items: center;
    gap: var(--space-8);
    align-self: start;
    height: var(--control-height-large);
    margin-top: calc(var(--leading-label) + var(--space-6));
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .check > input {
    flex: none;
    width: 16px;
    height: 16px;
    margin: 0;
    accent-color: var(--text-1);
  }

  @media (max-width: 720px) {
    .line {
      flex-wrap: wrap;
    }

    .words {
      flex-basis: 100%;
    }

    .filters {
      grid-template-columns: minmax(0, 1fr);
    }

    .check {
      height: var(--control-height-touch);
      margin-top: 0;
    }
  }
</style>
