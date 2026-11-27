<script lang="ts">
  import { tick } from "svelte";

  import { goto, refreshAll } from "$app/navigation";
  import { page } from "$app/state";

  import type { ApiResult, MessagePage } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import HistoryDrawer from "#lib/components/HistoryDrawer.svelte";
  import ListHeader from "#lib/components/ListHeader.svelte";
  import MessageTable from "#lib/components/MessageTable.svelte";
  import { pastTheEndHref, wantedPage } from "#lib/list/pages.js";
  import { signInPath } from "#lib/session.js";

  import type { PageProps } from "./$types";

  // The messages processing read only in part or couldn't read. It is the
  // message list without search and import, so it pages, refreshes, and
  // opens histories the same way.

  let { data }: PageProps = $props();

  let heading = $state<HTMLHeadingElement>();

  type Settled = { pageParam: string | null; result: ApiResult<MessagePage> };
  let settled = $state<Settled | null>(null);

  const list = $derived(
    settled !== null && settled.pageParam === data.pageParam
      ? settled.result
      : null,
  );

  $effect(() => {
    const { pageParam, messages } = data;
    let current = true;
    void messages.then(async (result) => {
      if (!current) return;
      const before = settled?.result.kind;
      settled = { pageParam, result };
      if (result.kind === "session_ended") {
        await goto(signInPath(page.url));
        return;
      }
      if (result.kind === "ok") {
        const href = pastTheEndHref(page.url, result.value);
        if (href !== null) await goto(href, { replace: true });
      }
      if (rowToFocus !== null) {
        const id = rowToFocus;
        rowToFocus = null;
        await tick();
        if (document.activeElement === document.body) focusRow(id);
      }
      if (before === "unavailable" && result.kind === "ok") {
        await tick();
        if (document.activeElement === document.body) heading?.focus();
      }
    });
    return () => {
      current = false;
    };
  });

  async function retryList() {
    await refreshAll();
    await data.messages;
  }

  const wanted = $derived(wantedPage(data.pageParam));

  const title = $derived(
    wanted !== null && wanted > 1
      ? `Processing problems, page ${data.pageParam}`
      : "Processing problems",
  );

  const pager = $derived.by(() => {
    if (data.session.kind !== "ok" || settled?.result.kind !== "ok") {
      return null;
    }
    if (list?.kind === "ok") {
      return list.value.items.length > 0 ? list.value : null;
    }
    if (list !== null || wanted === null) return null;
    const { pageSize, total } = settled.result.value;
    return (wanted - 1) * pageSize < total
      ? { page: wanted, pageSize, total }
      : null;
  });

  let rowToFocus: string | null = null;

  function focusRow(id: string): boolean {
    const row = document.getElementById(`history-${id}`);
    row?.focus();
    return row !== null;
  }

  async function closeHistory() {
    const url = new URL(page.url.href);
    const id = url.searchParams.get("message");
    if (id === null) return;
    if (document.activeElement === document.body && !focusRow(id)) {
      rowToFocus = id;
    }
    url.searchParams.delete("message");
    await goto(url.pathname + url.search, { reset: false });
  }
</script>

<svelte:head>
  <title>{title} · MailArkiv</title>
</svelte:head>

<main>
  <ListHeader title="Processing problems" {pager} bind:heading />

  <div class="body">
    <p class="intro">
      Messages that processing read only in part, or couldn't read. Each
      original is kept as it arrived and downloads unchanged.
    </p>

    {#if data.session.kind !== "ok" || list?.kind === "unavailable"}
      <FailedLoad retry={retryList} />
    {:else if list?.kind === "ok" && list.value.total === 0}
      <p class="empty">No processing problems.</p>
    {:else}
      <MessageTable
        items={list?.kind === "ok" && list.value.items.length > 0
          ? list.value.items
          : null}
        label="Processing problems"
      />
    {/if}
  </div>
</main>

<HistoryDrawer history={data.history} onclose={closeHistory} />

<style>
  .body {
    display: grid;
    gap: var(--space-16);
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  .intro,
  .empty {
    color: var(--text-3);
  }

  @media (max-width: 720px) {
    .body {
      padding: var(--space-16);
    }
  }
</style>
