<script lang="ts">
  import type { Source } from "#lib/api.js";
  import Facts from "#lib/components/Facts.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatTime } from "#lib/format/time.js";
  import { problemSummary, SECURITY_WORDS } from "#lib/sources/describe.js";

  // The facts the list and a source's page share. The page lists folders in
  // their own section, so it passes none. The list names the mailbox under
  // the source's name, so it leaves out Server and User name.

  let {
    source,
    folders = null,
    problems = false,
    mailbox = true,
  }: {
    source: Omit<Source, "folders">;
    folders?: string[] | null;
    /** Add a row naming the problem counts, if any. */
    problems?: boolean;
    /** Show the Server and User name rows. */
    mailbox?: boolean;
  } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const summary = $derived(problems ? problemSummary(source.problems) : null);
</script>

<Facts>
  {#if mailbox}
    <div>
      <dt>Server</dt>
      <dd>
        <UntrustedText value={source.host} />, port {source.port},
        {SECURITY_WORDS[source.security]}
      </dd>
    </div>
    <div>
      <dt>User name</dt>
      <dd><UntrustedText value={source.userName} /></dd>
    </div>
  {/if}
  {#if folders !== null}
    <div>
      <dt>Folders</dt>
      <dd>
        <!-- The separator is an expression, so formatting can't drop its
             space. -->
        {#each folders as folder, index (index)}{#if index > 0}{", "}{/if}<UntrustedText
            value={folder}
          />{/each}
      </dd>
    </div>
  {/if}
  <div>
    <dt>Last successful sync</dt>
    <dd>
      {#if source.lastSuccess === null}
        None yet
      {:else}
        <time datetime={source.lastSuccess.at}
          >{formatTime(source.lastSuccess.at)}</time
        >, archived {numbers.format(source.lastSuccess.archived)}
      {/if}
    </dd>
  </div>
  <div>
    <dt>Collected</dt>
    <dd>{numbers.format(source.collected)}</dd>
  </div>
  {#if summary !== null}
    <div>
      <dt>Problems</dt>
      <!-- Warn, as on Status: the rest of the source still collects. -->
      <dd class="problems"><span class="dot"></span>{summary}</dd>
    </div>
  {/if}
</Facts>

<style>
  /* A line breaks before a time, never inside it. */
  time {
    white-space: nowrap;
  }

  .problems {
    display: flex;
    align-items: baseline;
    gap: var(--space-8);
  }

  /* Centred on the lowercase letters, as in the state line. */
  .dot {
    flex: none;
    align-self: center;
    width: var(--dot-size);
    height: var(--dot-size);
    border-radius: 50%;
    background: var(--state-warn);
  }
</style>
