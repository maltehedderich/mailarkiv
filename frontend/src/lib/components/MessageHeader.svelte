<script lang="ts">
  import ArchiveIcon from "~icons/lucide/archive";

  import type { Address, MessageContent, Observation } from "#lib/api.js";
  import Facts from "#lib/components/Facts.svelte";
  import FullHash from "#lib/components/FullHash.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatBytes } from "#lib/format/size.js";
  import { formatTime } from "#lib/format/time.js";
  import {
    archiveTimeReason,
    PROBLEM_NAMES,
    problemPart,
    STATES,
  } from "#lib/message/words.js";

  // The message's subject, header fields, date, and state. Every value from
  // the mail shows in full through UntrustedText, so nothing it holds can
  // pass for MailArkiv's own text.

  let {
    content,
    entry,
  }: {
    content: MessageContent;
    /** The newest entry, which names a message processing hasn't read. */
    entry: Observation | null;
  } = $props();

  const parsed = $derived(content.parsed);
  const archiveTime = $derived(parsed === null || content.dateReason !== null);
  const state = $derived(STATES[content.processing]);

  function sameAsAddress(address: Address): boolean {
    return (
      address.name !== null &&
      address.address !== null &&
      address.name.toLowerCase() === address.address.toLowerCase()
    );
  }

  const fields = $derived(
    parsed === null
      ? []
      : (
          [
            ["From", parsed.from],
            ["To", parsed.to],
            ["Cc", parsed.cc],
            ["Bcc", parsed.bcc],
          ] as const
        ).filter(([label, list]) => label === "From" || list.length > 0),
  );
</script>

{#snippet address(entry: Address)}
  <span class="address"
    >{#if entry.address === null || sameAsAddress(entry)}<span class="name"
        ><UntrustedText value={entry.address ?? entry.name ?? ""} full /></span
      >{:else}{#if entry.name !== null}<span class="name"
          ><UntrustedText value={entry.name} full /></span
        >{/if}<span class="mailbox"
        ><UntrustedText value={entry.address} full /></span
      >{/if}</span
  >
{/snippet}

<div class="header">
  <h2 class="subject">
    {#if parsed === null}
      <!-- Processing hasn't read the subject, so the capture data names the
           message, as its row does. -->
      {#if entry === null}
        {state.word}
      {:else if entry.sourceType === "manual_import"}
        {#if entry.fileName === null}<span class="missing">(no name)</span
          >{:else}<UntrustedText value={entry.fileName} full />{/if}
      {:else}
        <span class="collection"
          ><UntrustedText value={entry.source.name} full /><span class="folder"
            ><UntrustedText value={entry.folder} full /></span
          ></span
        >
      {/if}
    {:else if parsed.subject === null}
      <span class="missing">(no subject)</span>
    {:else}
      <UntrustedText value={parsed.subject} full />
    {/if}
  </h2>

  <Facts>
    {#each fields as [label, list] (label)}
      <div>
        <dt>{label}</dt>
        <dd class="list">
          {#each list as entry, index (index)}
            {@render address(entry)}
          {:else}
            <span class="missing">(no sender)</span>
          {/each}
        </dd>
      </div>
    {/each}
    <div>
      <dt>Date</dt>
      <dd>
        <span class="date"
          >{#if archiveTime}<span class="mark" title="Archive time"
              ><ArchiveIcon /><span class="visually-hidden">Archive time</span
              ></span
            >{/if}<time datetime={content.listDate}
            >{formatTime(content.listDate)}</time
          ></span
        >
        {#if archiveTime}
          <span class="note"
            >{archiveTimeReason(content.processing, content.dateReason)}</span
          >
        {/if}
      </dd>
    </div>
    {#if archiveTime && content.dateHeader !== null}
      <div>
        <dt>Date header</dt>
        <dd><UntrustedText value={content.dateHeader} full /></dd>
      </div>
    {/if}
    {#if content.processing !== "processed"}
      <div>
        <dt>State</dt>
        <dd class="list">
          <span class="state"
            ><span class="dot {state.tone}"></span>{state.word}</span
          >
          {#each content.problems as problem, index (index)}
            <span class="problem"
              ><span class="place"
                >{problemPart(problem)}{#if problem.declaredType !== null},
                  <UntrustedText value={problem.declaredType} />{/if}</span
              >
              <span class="kind">{PROBLEM_NAMES[problem.kind]}</span></span
            >
          {/each}
        </dd>
      </div>
    {/if}
    {#if parsed === null}
      <div>
        <dt>Size</dt>
        <dd class="numbers">{formatBytes(content.size)}</dd>
      </div>
      <div>
        <dt>Content hash</dt>
        <dd><FullHash hash={content.contentHash} /></dd>
      </div>
    {/if}
  </Facts>
</div>

<style>
  .header {
    display: grid;
    gap: var(--space-16);
  }

  .subject {
    margin: 0;
    color: var(--text-1);
    font-size: var(--size-title);
    font-weight: var(--weight-strong);
    line-height: var(--leading-title);
    letter-spacing: var(--tracking-title);
    overflow-wrap: anywhere;
  }

  .missing {
    color: var(--text-3);
    font-weight: var(--weight-regular);
  }

  /* As a row's entry name: weight and colour, which the names can't fake,
     mark where the source name ends. */
  .collection {
    display: flex;
    flex-wrap: wrap;
    column-gap: var(--space-8);
  }

  .folder {
    color: var(--text-2);
    font-weight: var(--weight-regular);
  }

  .list {
    display: grid;
    gap: var(--space-4);
  }

  /* As a row shows a sender, but whole: the name in medium --text-1, then
     the address in --text-2, each its own bdi. */
  .address {
    display: flex;
    flex-wrap: wrap;
    column-gap: var(--space-6);
    min-width: 0;
    overflow-wrap: anywhere;
  }

  .name {
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .mailbox {
    color: var(--text-2);
  }

  .date {
    display: inline-flex;
    gap: var(--space-4);
    align-items: center;
    color: var(--text-1);
  }

  .mark {
    display: inline-flex;
    color: var(--text-3);
  }

  .mark :global(svg) {
    width: var(--icon-size-small);
    height: var(--icon-size-small);
  }

  .note {
    display: block;
    color: var(--text-3);
  }

  .state {
    display: inline-flex;
    gap: var(--space-6);
    align-items: center;
    color: var(--text-1);
    font-weight: var(--weight-medium);
  }

  .dot {
    flex: none;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--text-4);
  }

  .dot.ok {
    background: var(--state-ok);
  }

  .dot.warn {
    background: var(--state-warn);
  }

  .problem {
    display: flex;
    flex-wrap: wrap;
    column-gap: var(--space-6);
  }

  .place {
    color: var(--text-2);
  }

  .kind {
    color: var(--text-1);
  }

  .numbers {
    font-variant-numeric: tabular-nums;
  }
</style>
