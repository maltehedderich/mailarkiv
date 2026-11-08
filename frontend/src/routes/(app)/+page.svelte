<script lang="ts">
  import { onDestroy, tick } from "svelte";

  import { beforeNavigate, goto, refreshAll } from "$app/navigation";
  import { page } from "$app/state";

  import { getSession, type ApiResult, type MessagePage } from "#lib/api.js";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import HistoryDrawer from "#lib/components/HistoryDrawer.svelte";
  import ImportBox from "#lib/components/ImportBox.svelte";
  import MessageRow from "#lib/components/MessageRow.svelte";
  import Pager from "#lib/components/Pager.svelte";
  import { historyHref } from "#lib/history.js";
  import { choose } from "#lib/import/choose.js";
  import { isBusy, type ImportStep } from "#lib/import/step.js";
  import { upload } from "#lib/import/upload.js";
  import { signInPath } from "#lib/session.js";
  import {
    dismissToast,
    IMPORT_KEY,
    showToast,
  } from "#lib/toasts/store.svelte.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  let heading: HTMLHeadingElement;
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
        if (document.activeElement === document.body) heading.focus();
      }
    });
    return () => {
      current = false;
    };
  });

  // The list streams, so the load settles before the list arrives. Try again
  // waits for the list too, or a second press would send a second request.
  async function retryList() {
    await refreshAll();
    await data.messages;
  }

  /**
   * A page past the end, from an old link or a typed address, moves back.
   * Page 1 has no parameter, as in the pager's links.
   */
  async function movePastTheEnd({ items, total, pageSize }: MessagePage) {
    if (items.length > 0 || total === 0) return;
    const url = new URL(page.url.href);
    const last = Math.ceil(total / pageSize);
    if (last === 1) url.searchParams.delete("page");
    else url.searchParams.set("page", String(last));
    await goto(url.pathname + url.search, { replace: true });
  }

  /** The page the address asks for, or null for a value the server refuses. */
  const wanted = $derived(
    data.pageParam === null
      ? 1
      : /^[1-9]\d*$/.test(data.pageParam)
        ? Number(data.pageParam)
        : null,
  );

  // SvelteKit announces the title after each navigation, and only when it
  // changes, so the page number makes Previous and Next heard. It comes from
  // the address, because the list may still be loading when the title is read.
  const title = $derived(
    wanted !== null && wanted > 1
      ? `Messages, page ${data.pageParam}`
      : "Messages",
  );

  // While another page loads, the pager shows it at once, from the address
  // and the last total. The turn shows where it went before the rows arrive,
  // and on a slow archive the arrows stay put for the next one. A page past
  // the end has no range, and moves back once its answer arrives.
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
  <!-- Sticky, with the pager, so the next page is in view wherever the list
       is scrolled. A turn goes to the top of the page. Below the list, the
       next Next was then 2,100 px down at 1280 px wide, and 4,000 at 360. -->
  <header class="messages-header">
    <h1 bind:this={heading} tabindex="-1">Messages</h1>
    {#if pager !== null}
      <Pager page={pager.page} pageSize={pager.pageSize} total={pager.total} />
    {/if}
  </header>

  <div class="body">
    <ImportBox {step} onchoose={chooseFiles} onimport={startImport} />

    {#if data.session.kind !== "ok" || list?.kind === "unavailable"}
      <FailedLoad retry={retryList} />
    {:else if list?.kind === "ok" && list.value.total === 0}
      <p class="empty">No mail yet</p>
    {:else}
      <div class="table">
        <p class="heads" aria-hidden="true">
          <span>File name</span>
          <span>Imported</span>
          <span class="right">Size</span>
          <span>Hash</span>
        </p>
        {#if list?.kind === "ok" && list.value.items.length > 0}
          <ul aria-label="Messages">
            {#each list.value.items as message (message.id)}
              <MessageRow
                {message}
                historyHref={historyHref(page.url, message.id)}
              />
            {/each}
          </ul>
        {:else}
          <!-- Loading, or moving back from past the end. Still, so a slow
               archive doesn't look busy. -->
          <ul class="skeleton" aria-hidden="true">
            {#each { length: 6 }, index (index)}
              <li>
                <span class="bone name"></span>
                <span class="bone time"></span>
                <span class="bone size"></span>
                <span class="bone hash"></span>
              </li>
            {/each}
          </ul>
        {/if}
      </div>
    {/if}
  </div>
</main>

<HistoryDrawer history={data.history} onclose={closeHistory} />

<style>
  /* Above the rows' raised downloads as they scroll under it. Its corners
     follow the panel's, which a square fill would cover. */
  header {
    position: sticky;
    top: 0;
    z-index: 2;
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: var(--space-16);
    height: var(--header-height);
    padding: 0 var(--space-24);
    border-bottom: 1px solid var(--line);
    border-radius: var(--radius-large) var(--radius-large) 0 0;
    background: var(--panel);
  }

  /* WCAG 2.4.11: focus scrolls a control clear of the header. */
  :global(html:has(.messages-header)) {
    scroll-padding-top: var(--header-height);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  /* Focused only when the content it heads was replaced. */
  h1:focus {
    outline: none;
  }

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

  /* The columns from DESIGN.md. 185 px fits the widest time, such as
     "22 May 2026, 09:14:55 +05:30", in Inter at 12 px with tabular numerals.
     A subgrid's padding doesn't shrink a fixed column, so an edge column
     includes the rows' padding: 4 px after the download. */
  .table {
    display: grid;
    grid-template-columns:
      minmax(0, 1fr) 185px 62px 72px 40px
      calc(var(--control-height) + var(--space-4));
    column-gap: var(--space-12);
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

  .bone.name {
    width: 60%;
  }

  /* DESIGN.md: beside the sidebar, one line would leave the name no room: at
     721 px the table is 441 px wide and the fixed columns take 451. So rows
     take two lines. The name spans the first three columns, and time, size,
     and hash keep their order below it. Time comes first here, so its column
     includes the rows' 10 px left padding. The hash, 58 px wide, takes the
     flexible column, which is 64 px at 721 px. MessageRow places its cells,
     and the skeleton matches its height. */
  @media (720px < width <= 1040px) {
    .table {
      grid-template-columns:
        calc(185px + var(--space-10)) 62px minmax(0, 1fr) 40px
        calc(var(--control-height) + var(--space-4));
    }

    .heads {
      display: none;
    }

    .skeleton li {
      grid-template-rows: var(--leading-body) var(--leading-label);
      row-gap: var(--space-2);
      height: auto;
      padding-block: var(--space-8);
    }

    .bone.name {
      grid-row: 1;
      grid-column: 1 / 4;
    }

    .bone.time,
    .bone.size,
    .bone.hash {
      grid-row: 2;
    }

    .bone.hash {
      width: 56px;
    }
  }

  /* DESIGN.md: the header row goes, and rows stack. The skeleton keeps the
     stacked row's three lines, with the last two as tall as the download
     beside them, so the list doesn't jump when it arrives. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
      border-radius: 0;
    }

    .body {
      padding: var(--space-16);
    }

    .table {
      display: block;
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
        "name name name"
        "time size ."
        "hash . .";
      gap: var(--space-2) var(--space-12);
      height: auto;
      padding: var(--space-8) var(--space-4) var(--space-8) var(--space-8);
    }

    .skeleton li:not(:last-child) {
      border-bottom: 1px solid var(--line);
    }

    .bone.name {
      grid-area: name;
    }

    .bone.time {
      grid-area: time;
      width: 50%;
    }

    .bone.size {
      grid-area: size;
      width: 40px;
    }

    .bone.hash {
      grid-area: hash;
      width: 64px;
    }
  }
</style>
