<script lang="ts">
  import type { Source } from "#lib/api.js";
  import Facts from "#lib/components/Facts.svelte";
  import UntrustedText from "#lib/components/UntrustedText.svelte";
  import { formatTime } from "#lib/format/time.js";
  import { problemSummary, SECURITY_WORDS } from "#lib/sources/describe.js";

  // The facts the list and a source's page share. The page lists folders in
  // their own section, so it passes none.

  let {
    source,
    folders = null,
    problems = false,
  }: {
    source: Omit<Source, "folders">;
    folders?: string[] | null;
    /** Add a row naming the problem counts, if any. */
    problems?: boolean;
  } = $props();

  const numbers = new Intl.NumberFormat("en-US");

  const summary = $derived(problems ? problemSummary(source.problems) : null);
</script>

<Facts>
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
      <dd>{summary}</dd>
    </div>
  {/if}
</Facts>
