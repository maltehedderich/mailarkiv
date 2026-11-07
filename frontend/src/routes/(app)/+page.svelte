<script lang="ts">
  import { onDestroy, tick } from "svelte";

  import { beforeNavigate, goto } from "$app/navigation";
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

  let box: ReturnType<typeof ImportBox>;
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

  // SvelteKit announces the title after each navigation, and only when it
  // changes, so the page number makes Previous and Next heard. It comes from
  // the address, because the list may still be loading when the title is read.
  const title = $derived(
    /^[1-9]\d*$/.test(data.pageParam ?? "") && data.pageParam !== "1"
      ? `Messages, page ${data.pageParam}`
      : "Messages",
  );

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
    // FR-032: the last result stays until the next import starts.
    dismissToast(IMPORT_KEY);
    step = { kind: "uploading", file, fraction: 0 };
    // FR-040: a session that ended opens sign-in before any byte is sent.
    // Only a marked 401 changes anything. After any other result the upload
    // goes ahead, and its own answer decides the outcome.
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
    step = { kind: "ready" };
    showToast({ kind: "import", fileName: file.name, result });
    // Import goes away with the chosen file, so focus moves on purpose.
    await tick();
    if (!mounted) return;
    box.focus();
    // The newest message is first, so the list goes back to page 1. An open
    // history stays open.
    const url = new URL(page.url.href);
    url.searchParams.delete("page");
    await goto(url.pathname + url.search, { refreshAll: true, reset: false });
  }

  // FR-037: closing the tab may stop the upload. In-app navigation lets an
  // upload finish and report through the shared toast store.
  beforeNavigate((navigation) => {
    if (!busy || navigation.type === "leave") return;
    if (navigation.to?.url.pathname === page.url.pathname) return;
    if (!confirm(LEAVE)) navigation.cancel();
  });

  function warnBeforeUnload(event: BeforeUnloadEvent) {
    if (busy) event.preventDefault();
  }

  // The dialog has closed, by Escape, Close, or Back.
  async function closeHistory() {
    const url = new URL(page.url.href);
    const id = url.searchParams.get("message");
    // Back has already left this message's address.
    if (id === null) return;
    // The dialog gives focus back by itself, unless nothing had focus when it
    // opened: after a direct address, or a click in a browser that doesn't
    // focus links.
    if (document.activeElement === document.body) {
      document.getElementById(`history-${id}`)?.focus();
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
  <header>
    <h1 bind:this={heading} tabindex="-1">Messages</h1>
  </header>

  <div class="body">
    <ImportBox
      bind:this={box}
      {step}
      onchoose={chooseFiles}
      onimport={startImport}
    />

    {#if data.session.kind !== "ok" || list?.kind === "unavailable"}
      <FailedLoad />
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
      {#if list?.kind === "ok" && list.value.items.length > 0}
        {@const { page: number, pageSize, total } = list.value}
        <Pager page={number} {pageSize} {total} />
      {/if}
    {/if}
  </div>
</main>

<HistoryDrawer history={data.history} onclose={closeHistory} />

<style>
  header {
    display: flex;
    align-items: center;
    height: var(--header-height);
    padding: 0 var(--space-24);
    border-bottom: 1px solid var(--line);
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
     includes the rows' padding: 4 px after the download, which then ends
     where the pager's Next does. */
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
     stacked row's three lines, so the list doesn't jump when it arrives. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
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
      grid-template-columns: minmax(0, 1fr) auto;
      grid-template-rows:
        var(--leading-body) var(--leading-body)
        var(--control-height-touch);
      grid-template-areas:
        "name ."
        "time size"
        "hash .";
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
