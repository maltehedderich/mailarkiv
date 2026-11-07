<script lang="ts">
  import ArrowLeftIcon from "~icons/lucide/arrow-left";

  import Standalone from "#lib/components/Standalone.svelte";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();
</script>

<svelte:head>
  <title>Download failed · MailArkiv</title>
</svelte:head>

<Standalone title="This message wasn't downloaded">
  <div class="message">
    <p class="reason">{data.reason}</p>
    <p class="next">{data.nextStep}</p>
  </div>
  <a class="button back" href="/"><ArrowLeftIcon /> Back to the list</a>
</Standalone>

<style>
  .message {
    display: grid;
    gap: var(--space-4);
  }

  .reason {
    color: var(--text-2);
  }

  .next {
    color: var(--text-3);
  }

  .back {
    justify-self: start;
  }
</style>
