<script lang="ts">
  import { onDestroy, tick } from "svelte";

  import { beforeNavigate, goto, refreshAll, snapshot } from "$app/navigation";
  import { page } from "$app/state";

  import {
    getSession,
    SEARCH_MAX_PAGE,
    searchMessages,
    type ApiResult,
    type MessagePage,
    type SearchPage,
    type SearchQuery,
    type SearchResult,
  } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import HistoryDrawer from "#lib/components/HistoryDrawer.svelte";
  import ImportBox from "#lib/components/ImportBox.svelte";
  import ListHeader from "#lib/components/ListHeader.svelte";
  import MessageTable from "#lib/components/MessageTable.svelte";
  import ResultsHeader from "#lib/components/ResultsHeader.svelte";
  import SearchForm from "#lib/components/SearchForm.svelte";
  import { choose } from "#lib/import/choose.js";
  import { isBusy, type ImportStep } from "#lib/import/step.js";
  import { upload } from "#lib/import/upload.js";
  import { pastTheEndHref, wantedPage } from "#lib/list/pages.js";
  import {
    emptyForm,
    filterCount,
    readForm,
    type SearchForm as Form,
  } from "#lib/search/form.js";
  import { signInPath } from "#lib/session.js";
  import {
    dismissToast,
    IMPORT_KEY,
    showToast,
  } from "#lib/toasts/store.svelte.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  let heading = $state<HTMLHeadingElement>();
  let mounted = true;
  onDestroy(() => {
    mounted = false;
  });

  // The page owns the import. Its result is a toast, so it outlives a failed
  // refresh and a visit to another page.
  let step = $state<ImportStep>({ kind: "ready" });
  const busy = $derived(isBusy(step));

  const LEAVE =
    "An import is still running. If you leave now, the file may not be archived.";

  // The latest list that arrived, with the page it is for. A refresh of the
  // same page keeps showing it, so the rows, and the focus in them, stay until
  // the new list replaces them.
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
      if (result.kind === "ok") await movePastTheEnd(result.value);
      // The history closed before its row arrived. Anything focused since
      // keeps focus.
      if (rowToFocus !== null) {
        const id = rowToFocus;
        rowToFocus = null;
        await tick();
        if (document.activeElement === document.body) focusRow(id);
      }
      // Try again went away with the failed-load block, so focus moves on
      // purpose instead of falling to the page.
      if (before === "unavailable" && result.kind === "ok") {
        await tick();
        if (document.activeElement === document.body) heading?.focus();
      }
    });
    return () => {
      current = false;
    };
  });

  // A search shows in place of the list. It lives only in this page's state,
  // never in its address, so the words stay out of the browser's history and
  // any proxy's log.
  let form = $state<Form>(emptyForm());
  let filtersOpen = $state(false);
  /** Why the days can't be searched, from the last submit. */
  let dayProblem = $state<string | null>(null);
  type Search = {
    /** What was submitted, at the page asked for. */
    query: SearchQuery;
    /** The answer for that page, or null while it loads. */
    result: SearchResult | null;
    /** The last page this search answered, kept while another loads. */
    shown: SearchPage | null;
  };
  let search = $state<Search | null>(null);
  /** Counts searches, so only the last one's answer shows. */
  let searches = 0;

  async function runSearch(query: SearchQuery, shown: SearchPage | null) {
    const run = ++searches;
    search = { query, result: null, shown };
    const result = await searchMessages(query);
    if (run !== searches || !mounted) return;
    if (result.kind === "session_ended") {
      await goto(signInPath(page.url));
      return;
    }
    search = {
      query,
      result,
      shown: result.kind === "ok" ? result.value : shown,
    };
  }

  function submitSearch() {
    const reading = readForm(form);
    dayProblem = reading.kind === "invalid" ? reading.problem : null;
    if (reading.kind === "invalid") return;
    if (reading.kind === "list") {
      searches++;
      search = null;
      return;
    }
    void runSearch(reading.query, null);
  }

  // A turn keeps what was submitted, not what the form holds now.
  function turnSearch(to: number) {
    if (search === null) return;
    window.scrollTo({ top: 0 });
    void runSearch({ ...search.query, page: to }, search.shown);
  }

  function clearSearch() {
    searches++;
    form = emptyForm();
    dayProblem = null;
    search = null;
  }

  const canClear = $derived(
    search !== null ||
      dayProblem !== null ||
      form.words !== "" ||
      filterCount(form) > 0,
  );

  // Back to this page, from another page or a message's history, shows the
  // same search without asking again. SvelteKit keeps this in the tab's
  // session storage.
  snapshot({
    capture: () => ({
      form: $state.snapshot(form),
      filtersOpen,
      search: $state.snapshot(search),
    }),
    restore: (value) => {
      searches++;
      form = value.form;
      filtersOpen = value.filtersOpen;
      dayProblem = null;
      search = value.search;
      // Left before the answer came, so ask again.
      if (value.search !== null && value.search.result === null) {
        void runSearch(value.search.query, value.search.shown);
      }
    },
  });

  // The list streams, so the load settles before the list arrives. Try again
  // waits for the list too, or a second press would send a second request.
  async function retryList() {
    await refreshAll();
    await data.messages;
  }

  async function movePastTheEnd(value: MessagePage) {
    const href = pastTheEndHref(page.url, value);
    if (href !== null) await goto(href, { replace: true });
  }

  const wanted = $derived(wantedPage(data.pageParam));

  // SvelteKit announces the title after each navigation, and only when it
  // changes, so the page number makes Previous and Next heard. It comes from
  // the address, because the list may still be loading when the title is read.
  const title = $derived.by(() => {
    if (search !== null) {
      return search.query.page > 1
        ? `Search results, page ${search.query.page}`
        : "Search results";
    }
    return wanted !== null && wanted > 1
      ? `Messages, page ${data.pageParam}`
      : "Messages";
  });

  // A search pages in this page's state, so its page stays out of the
  // address. The range shows the page asked for at once, from the last total.
  const searchPager = $derived.by(() => {
    if (search === null || search.shown === null) return null;
    const { pageSize, total } = search.shown;
    if (total === 0) return null;
    return {
      page: search.query.page,
      pageSize,
      total,
      label: "Search results",
      maxPage: SEARCH_MAX_PAGE,
      onturn: turnSearch,
    };
  });

  // While another page loads, the pager shows it at once, from the address
  // and the last total. The turn shows where it went before the rows arrive,
  // and on a slow archive the arrows stay put for the next one. A page past
  // the end has no range, and moves back once its answer arrives.
  const pager = $derived.by(() => {
    if (data.session.kind !== "ok" || settled?.result.kind !== "ok") {
      return null;
    }
    if (list?.kind === "ok") {
      const { page, pageSize, total, items } = list.value;
      return items.length > 0 ? { page, pageSize, total } : null;
    }
    if (list !== null || wanted === null) return null;
    const { pageSize, total } = settled.result.value;
    return (wanted - 1) * pageSize < total
      ? { page: wanted, pageSize, total }
      : null;
  });

  function chooseFiles(files: File[]) {
    const choice = choose(files);
    if (choice.kind === "chosen") step = { kind: "chosen", file: choice.file };
    if (choice.kind === "refused") {
      step = { kind: "refused", reason: choice.reason };
    }
  }

  async function startImport() {
    if (step.kind !== "chosen") return;
    const { file } = step;
    // The last result stays until the next import starts.
    dismissToast(IMPORT_KEY);
    step = { kind: "uploading", file, fraction: 0 };
    // A session that ended opens sign-in before any byte is sent. Only a
    // marked 401 changes anything. After any other result the upload goes
    // ahead, and its own answer decides the outcome.
    const session = await getSession();
    if (!mounted) return;
    if (session.kind === "session_ended") {
      step = { kind: "chosen", file };
      await goto(signInPath(page.url));
      return;
    }
    const result = await upload(file, {
      onProgress: (fraction) => {
        if (step.kind === "uploading") {
          step = { kind: "uploading", file, fraction };
        }
      },
      onUploaded: () => {
        step = { kind: "storing", file };
      },
    });
    if (result.outcome === "session_ended") {
      if (!mounted) return;
      step = { kind: "chosen", file };
      await goto(signInPath(page.url));
      return;
    }
    // The box moves focus to Choose a file if Import had it, and leaves focus
    // anywhere else alone.
    step = { kind: "ready" };
    showToast({ kind: "import", fileName: file.name, result });
    // Left for another page, so this one doesn't navigate.
    if (!mounted) return;
    // The newest message is first, so the list goes back to page 1. An open
    // history stays open.
    const url = new URL(page.url.href);
    url.searchParams.delete("page");
    await goto(url.pathname + url.search, { refreshAll: true, reset: false });
  }

  // Closing the tab may stop the upload. In-app navigation lets an upload
  // finish and report through the shared toast store.
  beforeNavigate((navigation) => {
    if (!busy || navigation.type === "leave") return;
    if (navigation.to?.url.pathname === page.url.pathname) return;
    if (!confirm(LEAVE)) navigation.cancel();
  });

  function warnBeforeUnload(event: BeforeUnloadEvent) {
    if (busy) event.preventDefault();
  }

  /** A message whose row gets focus once the list arrives. */
  let rowToFocus: string | null = null;

  function focusRow(id: string): boolean {
    const row = document.getElementById(`history-${id}`);
    row?.focus();
    return row !== null;
  }

  // The dialog has closed, by Escape, Close, Back, or a click on the scrim.
  async function closeHistory() {
    const url = new URL(page.url.href);
    const id = url.searchParams.get("message");
    // Back has already left this message's address.
    if (id === null) return;
    // The dialog gives focus back by itself, unless nothing had focus when it
    // opened: after a direct address, or a click in a browser that doesn't
    // focus links. A direct address streams the list, so the row may not be
    // there yet, and gets focus when it arrives.
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

<svelte:window onbeforeunload={warnBeforeUnload} />

<main>
  <ListHeader
    title="Messages"
    pager={search === null ? pager : searchPager}
    bind:heading
  />

  <div class="body">
    <SearchForm
      bind:form
      bind:open={filtersOpen}
      {dayProblem}
      {canClear}
      onsearch={submitSearch}
      onclear={clearSearch}
    />

    <ImportBox {step} onchoose={chooseFiles} onimport={startImport} />

    {#if search !== null}
      <ResultsHeader
        result={search.result}
        shown={search.shown}
        maxResults={SEARCH_MAX_PAGE * (search.shown?.pageSize ?? 0)}
      />
      {#if search.result === null}
        <MessageTable items={null} label="Search results" />
      {:else if search.result.kind === "ok" && search.result.value.items.length > 0}
        <MessageTable
          items={search.result.value.items}
          label="Search results"
        />
      {/if}
    {:else if data.session.kind !== "ok" || list?.kind === "unavailable"}
      <FailedLoad retry={retryList} />
    {:else if list?.kind === "ok" && list.value.total === 0}
      <p class="empty">No mail yet</p>
    {:else}
      <MessageTable
        items={list?.kind === "ok" && list.value.items.length > 0
          ? list.value.items
          : null}
        label="Messages"
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

  .empty {
    color: var(--text-3);
  }

  @media (max-width: 720px) {
    .body {
      padding: var(--space-16);
    }
  }
</style>
