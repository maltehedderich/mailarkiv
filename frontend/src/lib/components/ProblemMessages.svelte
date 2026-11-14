<script lang="ts">
  import type { MessageProblemCategory, ProblemList } from "#lib/api.js";
  import CategoryFacts from "#lib/components/CategoryFacts.svelte";
  import Facts from "#lib/components/Facts.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";
  import { CATEGORY_WORDS } from "#lib/sources/words.js";

  // One of a source's message lists, as a section of its page. It names each
  // message by folder, size, and times only, never by its content.

  let {
    id,
    title,
    list,
  }: {
    /** The heading's ID, unique on the page. */
    id: string;
    title: string;
    list: ProblemList;
  } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  // Each reason in the list, once, in the order it first appears.
  const categories = $derived([
    ...new Set<MessageProblemCategory>(
      list.items.map((message) => message.category),
    ),
  ]);
</script>

<section aria-labelledby={id}>
  <h2 {id}>
    {title}
    {#if list.total > 0}
      <span class="count">{numbers.format(list.total)}</span>
    {/if}
  </h2>
  <div class="content">
    {#if list.total === 0}
      <p class="none">None.</p>
    {:else}
      <ol class="messages">
        {#each list.items as message, index (index)}
          <li>
            <p class="line">
              <span class="folder"
                ><span class="visually-hidden">{"Folder "}</span><UntrustedText
                  value={message.folder}
                /></span
              >
              <span class="reason"
                >{CATEGORY_WORDS[message.category].label}</span
              >
            </p>
            <p class="note">
              <span
                >{message.size === null
                  ? "Size unknown"
                  : formatBytes(message.size)}</span
              >
              <span
                >{#if message.receivedAt === null}Received time unknown{:else}Received
                  <time datetime={message.receivedAt}
                    >{formatTime(message.receivedAt)}</time
                  >{/if}</span
              >
              <span
                >Recorded <time datetime={message.recordedAt}
                  >{formatTime(message.recordedAt)}</time
                ></span
              >
            </p>
          </li>
        {/each}
      </ol>
      {#if list.total > list.items.length}
        <p class="more">
          Showing the newest {numbers.format(list.items.length)} of
          {numbers.format(list.total)}.
        </p>
      {/if}
      {#each categories as category (category)}
        <div class="words">
          <h3>{CATEGORY_WORDS[category].label}</h3>
          <Facts><CategoryFacts {category} /></Facts>
        </div>
      {/each}
    {/if}
  </div>
</section>

<style>
  .count {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  .none {
    color: var(--text-1);
  }

  /* Sized by the column, so the list knows when its facts fit on a line. */
  .content {
    container-type: inline-size;
  }

  /* One track that may shrink, so a long folder name is cut, not widening
     the page. */
  .messages {
    display: grid;
    grid-template-columns: minmax(0, 1fr);
    gap: var(--space-8);
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .line {
    display: flex;
    flex-wrap: wrap;
    gap: 0 var(--space-12);
  }

  /* A folder name is untrusted text in --text-1, cut in its middle when the
     line is too narrow. */
  .folder {
    display: inline-flex;
    min-width: 0;
    max-width: 100%;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .reason {
    color: var(--text-2);
  }

  .note,
  .more {
    color: var(--text-3);
    font-size: var(--size-label);
    font-variant-numeric: tabular-nums;
    line-height: var(--leading-label);
  }

  /* Each fact on its own line, so every message reads the same. Wrapping
     put a short size beside its time and a long one above it. */
  .note {
    display: grid;
  }

  /* With room, the size takes a column the whole list shares and the two
     times sit one above the other, so they can be compared. 112 px holds a
     size up to the 150,000,000-byte limit, so both lists on the page share
     the column. */
  @container (min-width: 380px) {
    .messages {
      grid-template-columns: minmax(112px, max-content) minmax(0, 1fr);
      column-gap: var(--space-16);
    }

    .messages > li,
    .note {
      display: grid;
      grid-template-columns: subgrid;
      grid-column: 1 / -1;
    }

    .line {
      grid-column: 1 / -1;
    }

    .note > :last-child {
      grid-column: 2;
    }
  }

  /* With room for all three on a line, each has its own column. */
  @container (min-width: 620px) {
    .messages {
      grid-template-columns:
        minmax(112px, max-content)
        max-content minmax(0, 1fr);
    }

    .note > :last-child {
      grid-column: auto;
    }
  }

  .words {
    display: grid;
    gap: var(--space-4);
    margin-top: var(--space-8);
  }

  h3 {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }
</style>
