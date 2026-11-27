<script lang="ts">
  import ChevronLeftIcon from "~icons/lucide/chevron-left";
  import ChevronRightIcon from "~icons/lucide/chevron-right";

  import { tick, untrack } from "svelte";

  import { afterNavigate, goto } from "$app/navigation";
  import { page } from "$app/state";

  import {
    getMessage,
    getMessageText,
    type ApiResult,
    type ContentResult,
    type MessageContent,
    type MessageHistory,
  } from "#lib/api.js";
  import AttachmentList from "#lib/components/AttachmentList.svelte";
  import FailedLoad from "#lib/components/FailedLoad.svelte";
  import HistoryDrawer from "#lib/components/HistoryDrawer.svelte";
  import ImageNotice from "#lib/components/ImageNotice.svelte";
  import MessageHeader from "#lib/components/MessageHeader.svelte";
  import PlainText from "#lib/components/PlainText.svelte";
  import PreviewFrame from "#lib/components/PreviewFrame.svelte";
  import { startDownload } from "#lib/download/start.js";
  import { entryName } from "#lib/format/entry.js";
  import {
    BODY_CUT,
    bodyFor,
    EMBEDDED_LEFT_OUT,
    frameAddress,
    loadImages,
    NO_TEXT,
    notices,
    showBody,
    type Body,
    type BodyView,
  } from "#lib/message/view.js";
  import { CONTENT_FAILED, NOT_READ } from "#lib/message/words.js";
  import { signInPath } from "#lib/session.js";

  import type { PageProps } from "./$types";

  let { data }: PageProps = $props();

  const content = $derived(
    data.content.kind === "ok" ? data.content.value : null,
  );
  const failure = $derived(
    data.content.kind === "failed"
      ? CONTENT_FAILED[data.content.reason]
      : data.content.kind === "unavailable"
        ? CONTENT_FAILED.unavailable
        : null,
  );

  // The body's choices belong to this load: any new load, even of the same
  // message, starts with the HTML and without images from the internet. Raw,
  // so the content stays the object the load returned and compares equal.
  let kept = $state.raw<{ content: MessageContent; body: BodyView } | null>(
    null,
  );
  const body = $derived(content === null ? null : bodyFor(kept, content));

  function change(next: BodyView) {
    if (content !== null) kept = { content, body: next };
  }

  // The text body, fetched by the load when it shows first, or here when the
  // administrator switches to it.
  let fetchedText = $state.raw<{
    content: MessageContent;
    result: Promise<ContentResult<string>>;
  } | null>(null);
  const text = $derived(
    fetchedText !== null && fetchedText.content === content
      ? fetchedText.result
      : data.text,
  );

  function fetchText(): Promise<ContentResult<string>> {
    if (content === null) return Promise.resolve({ kind: "unavailable" });
    const result = getMessageText(content.id);
    fetchedText = { content, result };
    return result;
  }

  async function show(choice: Body) {
    if (body === null) return;
    change(showBody(body, choice));
    if (choice === "text" && text === null) void fetchText();
  }

  // Load images goes away with its notice, so focus moves to the reloaded
  // frame instead of falling to the page.
  let preview = $state<PreviewFrame | null>(null);
  async function showImages() {
    if (body === null) return;
    change(loadImages(body));
    await tick();
    preview?.focus();
  }

  $effect(() => {
    void text?.then(async (result) => {
      if (result.kind === "session_ended") await goto(signInPath(page.url));
    });
  });

  // The way up names the list the view came from and goes Back to it, so the
  // same page and results show. Otherwise it opens the message list.
  const LISTS: Record<string, string> = {
    "/": "Messages",
    "/problems": "Processing problems",
  };
  let cameFrom = $state<string | null>(null);
  afterNavigate(({ from, type }) => {
    if (type === "enter") return;
    if (from?.url.pathname === page.url.pathname) return;
    cameFrom =
      from !== null && from.url.pathname in LISTS ? from.url.pathname : null;
  });

  function up(event: MouseEvent) {
    if (cameFrom === null || event.button !== 0) return;
    if (event.ctrlKey || event.metaKey || event.shiftKey || event.altKey)
      return;
    event.preventDefault();
    history.back();
  }

  // The history opens over the view at ?history, so Back closes it. It's
  // fetched here, because a load reading the query would read the message
  // again and drop the choice to load images.
  const historyOpen = $derived(page.url.searchParams.has("history"));
  let historyResult = $state.raw<{
    id: string;
    result: ApiResult<MessageHistory>;
  } | null>(null);

  async function fetchHistory(id: string): Promise<void> {
    const result = await getMessage(id);
    if (result.kind === "session_ended") {
      await goto(signInPath(page.url));
      return;
    }
    historyResult = { id, result };
  }

  $effect(() => {
    if (!historyOpen) return;
    const id = data.id;
    if (untrack(() => historyResult?.id) === id) return;
    void fetchHistory(id);
  });
  const shownHistory = $derived(
    historyOpen && historyResult?.id === data.id ? historyResult.result : null,
  );

  /** Whether History added the drawer's entry to the tab's history. */
  let openedHere = false;

  function openHistory() {
    // Each opening fetches the history, so its entries are current.
    historyResult = null;
    openedHere = true;
    void goto(`${page.url.pathname}?history`, { reset: false });
  }

  async function closeHistory() {
    // Back has already left the drawer's address.
    if (!page.url.searchParams.has("history")) return;
    // Closing takes back the entry History added, so Messages, which goes
    // Back, reaches the list and not the drawer. A drawer opened by its
    // address has no entry before it here to go back to.
    if (openedHere) {
      openedHere = false;
      history.back();
      return;
    }
    await goto(page.url.pathname, { replace: true, reset: false });
  }

  // Try again went away with the failed-load block, so focus moves on purpose
  // instead of falling to the page.
  let heading: HTMLHeadingElement;
  let wasLoaded: boolean | null = null;
  $effect(() => {
    const loaded = content !== null;
    if (wasLoaded === false && loaded) {
      if (document.activeElement === document.body) heading.focus();
    }
    wasLoaded = loaded;
  });

  function download() {
    if (data.original === null) return;
    void startDownload({
      ...data.original,
      // The toast names the message as the view does.
      fileName:
        content?.parsed?.subject ??
        (data.entry === null ? null : entryName(data.entry)),
    });
  }

  const title = $derived(
    content?.parsed?.subject
      ? `${content.parsed.subject} · MailArkiv`
      : "Message · MailArkiv",
  );
</script>

<svelte:head>
  <title>{title}</title>
</svelte:head>

<main>
  <header>
    <div class="trail">
      <a class="up" href={cameFrom ?? "/"} onclick={up}
        ><ChevronLeftIcon class="icon back" /><span class="up-label"
          >{LISTS[cameFrom ?? "/"]}</span
        ></a
      >
      <ChevronRightIcon class="icon separator" />
      <h1 bind:this={heading} tabindex="-1">Message</h1>
    </div>
    <div class="actions">
      <button class="button" type="button" onclick={openHistory}>History</button
      >
      {#if data.original !== null}
        <button class="button" type="button" onclick={download}
          >Download original</button
        >
      {/if}
    </div>
  </header>

  <div class="body">
    {#if content !== null && body !== null}
      {@const parsed = content.parsed}
      <MessageHeader {content} entry={data.entry} />

      {#if parsed === null}
        <p class="quiet">{NOT_READ}</p>
      {:else}
        {@const shown = notices(body, parsed)}
        <section class="content" aria-label="Body">
          {#if body.choices.length > 1}
            <div class="choices" role="group" aria-label="Body">
              <button
                class="button"
                type="button"
                aria-pressed={body.shown === "html"}
                onclick={() => show("html")}>HTML</button
              >
              <button
                class="button"
                type="button"
                aria-pressed={body.shown === "text"}
                onclick={() => show("text")}>Plain text</button
              >
            </div>
          {/if}

          {#if shown.remoteImages}
            <ImageNotice onload={showImages} />
          {/if}
          {#if shown.embeddedImagesLeftOut}
            <div class="notice"><p class="what">{EMBEDDED_LEFT_OUT}</p></div>
          {/if}
          {#if shown.truncated}
            <div class="notice">
              <p class="what">{BODY_CUT.what}</p>
              <button class="button" type="button" onclick={download}
                >{BODY_CUT.action}</button
              >
            </div>
          {/if}

          {#if body.shown === "html"}
            <PreviewFrame bind:this={preview} src={frameAddress(body) ?? ""} />
          {:else if body.shown === "text" && text !== null}
            {#await text}
              <p class="quiet" role="status">Loading the text…</p>
            {:then result}
              {#if result.kind === "ok"}
                <PlainText text={result.value} />
              {:else if result.kind !== "session_ended"}
                {@const words =
                  result.kind === "failed"
                    ? CONTENT_FAILED[result.reason]
                    : CONTENT_FAILED.unavailable}
                <FailedLoad
                  retry={fetchText}
                  what={words.what}
                  next={words.next}
                />
              {/if}
            {/await}
          {:else}
            <p class="quiet">
              {NO_TEXT}{#if parsed.encrypted}
                It's encrypted. Its parts are in the attachment list.{/if}
            </p>
          {/if}
        </section>

        <AttachmentList
          messageId={content.id}
          attachments={parsed.attachments}
        />
      {/if}
    {:else if failure !== null}
      <FailedLoad what={failure.what} next={failure.next} />
    {/if}
  </div>
</main>

<HistoryDrawer
  history={shownHistory}
  onclose={closeHistory}
  retry={() => fetchHistory(data.id)}
/>

<style>
  header {
    display: flex;
    gap: var(--space-12);
    align-items: center;
    justify-content: space-between;
    height: var(--header-height);
    padding: 0 var(--header-inset);
    border-bottom: 1px solid var(--line);
  }

  .trail {
    display: flex;
    gap: var(--space-4);
    align-items: center;
    min-width: 0;
  }

  /* The chevron already marks it as the way up, so it needs no underline
     until it's pointed at. */
  .up {
    flex: none;
    color: var(--text-2);
    text-decoration: none;
  }

  .up:hover {
    color: var(--text-1);
    text-decoration: underline;
  }

  .up :global(.back) {
    display: none;
  }

  header :global(.separator) {
    flex: none;
    color: var(--text-3);
  }

  h1 {
    font-size: var(--size-section);
    font-weight: var(--weight-strong);
    letter-spacing: var(--tracking-section);
  }

  .actions {
    display: flex;
    flex: none;
    gap: var(--space-8);
  }

  .body {
    display: grid;
    gap: var(--space-24);
    max-width: var(--content-width);
    margin: 0 auto;
    padding: var(--space-24);
  }

  .content {
    display: grid;
    gap: var(--space-12);
    padding-top: var(--space-24);
    border-top: 1px solid var(--line);
  }

  .body > :global(.attachments) {
    padding-top: var(--space-24);
    border-top: 1px solid var(--line);
  }

  .choices {
    display: flex;
    gap: var(--space-4);
  }

  .choices [aria-pressed="true"] {
    background: var(--fill-3);
    color: var(--text-1);
  }

  .notice {
    display: grid;
    justify-items: start;
    gap: var(--space-8);
    padding: var(--space-12);
    border: 1px solid transparent;
    border-radius: var(--radius-medium);
    background: var(--fill-1);
  }

  .notice .what {
    color: var(--text-2);
  }

  .quiet {
    color: var(--text-3);
  }

  /* DESIGN.md: 16 px gutters, and the trail becomes a back arrow. */
  @media (max-width: 720px) {
    header {
      padding: 0 var(--space-16);
    }

    .body {
      padding: var(--space-16);
    }

    .trail {
      gap: 0;
    }

    .up {
      display: grid;
      place-items: center;
      width: var(--control-height-touch);
      height: var(--control-height-touch);
      margin-left: calc(
        -1 * (var(--control-height-touch) - var(--icon-size)) / 2
      );
      border-radius: var(--radius-medium);
      color: var(--text-2);
    }

    .up:hover {
      background: var(--fill-2);
    }

    .up :global(.back) {
      display: block;
    }

    .up-label {
      position: absolute;
      width: 1px;
      height: 1px;
      overflow: hidden;
      clip-path: inset(50%);
      white-space: nowrap;
    }

    header :global(.separator) {
      display: none;
    }
  }
</style>
