<script lang="ts">
  import type { Message } from "#lib/api.js";
  import MessageRow from "#lib/components/MessageRow.svelte";

  // A list's rows under its column heads, or still skeleton rows while they
  // load. Each row opens its message.

  let {
    items,
    label,
  }: {
    /** Null while loading, or while moving back from past the end. */
    items: Message[] | null;
    label: string;
  } = $props();
</script>

<div class="table">
  <p class="heads" aria-hidden="true">
    <span>Sender</span>
    <span>Subject</span>
    <span>Date</span>
    <span class="right">Size</span>
  </p>
  {#if items !== null}
    <ul aria-label={label}>
      {#each items as message (message.id)}
        <MessageRow {message} />
      {/each}
    </ul>
  {:else}
    <!-- Still, so a slow archive doesn't look busy. -->
    <ul class="skeleton" aria-hidden="true">
      {#each { length: 6 }, index (index)}
        <li>
          <span class="bone sender"></span>
          <span class="bone subject"></span>
          <span class="bone date"></span>
          <span class="bone size"></span>
        </li>
      {/each}
    </ul>
  {/if}
</div>

<style>
  /* The columns from DESIGN.md. 185 px fits the widest time, such as
     "22 May 2026, 09:14:55 +05:30", in Inter at 12 px with tabular numerals,
     and the archive time mark comes before it. A subgrid's padding doesn't
     shrink a fixed column, so an edge column includes the rows' padding: 4 px
     after the download. */
  .table {
    --date-width: calc(185px + var(--icon-size-small) + var(--space-4));

    display: grid;
    grid-template-columns:
      minmax(0, 1fr) minmax(0, 1fr) var(--date-width) 62px
      calc(var(--control-height) + var(--space-4));
    column-gap: var(--space-12);
    /* The rows' side padding reaches into the gutter, so names start on the
       header's edge and only the hover fill extends past it. */
    margin-inline: calc(-1 * var(--space-10)) calc(-1 * var(--space-4));
  }

  .heads,
  ul {
    display: grid;
    grid-template-columns: subgrid;
    grid-column: 1 / -1;
    margin: 0;
    padding: 0;
    list-style: none;
  }

  .heads {
    align-items: center;
    height: var(--space-32);
    padding: 0 var(--space-4) 0 var(--space-10);
    color: var(--text-3);
    font-size: var(--size-label);
    font-weight: var(--weight-medium);
    line-height: var(--leading-label);
  }

  .right {
    text-align: right;
  }

  .skeleton li {
    display: grid;
    grid-template-columns: subgrid;
    grid-column: 1 / -1;
    align-items: center;
    height: var(--row-height);
    padding: 0 var(--space-4) 0 var(--space-10);
  }

  .bone {
    height: 8px;
    border-radius: var(--radius-small);
    background: var(--fill-2);
  }

  .bone.sender,
  .bone.subject {
    width: 60%;
  }

  /* DESIGN.md: beside the sidebar, one line would leave the sender and
     subject no room: at 721 px the table is 441 px wide and the fixed
     columns take 330. So rows take two lines: the sender and date, then the
     subject and size. MessageRow places its cells, and the skeleton matches
     its height. */
  @media (720px < width <= 1040px) {
    .table {
      grid-template-columns:
        minmax(0, 1fr) var(--date-width)
        calc(var(--control-height) + var(--space-4));
    }

    .heads {
      display: none;
    }

    .skeleton li {
      grid-template-rows: var(--leading-body) var(--leading-body);
      row-gap: var(--space-2);
      height: auto;
      padding-block: var(--space-8);
    }

    .bone.sender,
    .bone.date {
      grid-row: 1;
    }

    .bone.subject,
    .bone.size {
      grid-row: 2;
    }

    .bone.sender,
    .bone.subject {
      grid-column: 1;
    }

    .bone.date,
    .bone.size {
      grid-column: 2;
      justify-self: end;
    }

    .bone.date {
      width: 140px;
    }

    .bone.size {
      width: 40px;
    }
  }

  /* DESIGN.md: the header row goes, and rows stack. The skeleton keeps the
     stacked row's three lines, with the last two as tall as the download
     beside them, so the list doesn't jump when it arrives. */
  @media (max-width: 720px) {
    .table {
      display: block;
      margin-inline: calc(-1 * var(--space-8)) calc(-1 * var(--space-4));
    }

    .heads {
      display: none;
    }

    ul {
      grid-template-columns: minmax(0, 1fr);
    }

    .skeleton li {
      grid-template-columns: minmax(0, 1fr) auto var(--control-height-touch);
      grid-template-rows:
        var(--leading-body)
        repeat(2, calc((var(--control-height-touch) - var(--space-2)) / 2));
      grid-template-areas:
        "sender sender sender"
        "subject . ."
        "date size .";
      gap: var(--space-2) var(--space-12);
      height: auto;
      padding: var(--space-8) var(--space-4) var(--space-8) var(--space-8);
    }

    .skeleton li:not(:last-child) {
      border-bottom: 1px solid var(--line);
    }

    .bone.sender {
      grid-area: sender;
    }

    .bone.subject {
      grid-area: subject;
    }

    .bone.date {
      grid-area: date;
      width: 50%;
    }

    .bone.size {
      grid-area: size;
      width: 40px;
    }
  }
</style>
