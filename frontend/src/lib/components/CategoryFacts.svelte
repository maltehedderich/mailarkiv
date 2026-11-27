<script lang="ts">
  import type { CollectionCategory } from "#lib/api.js";
  import { CATEGORY_WORDS } from "#lib/sources/words.js";

  // A category's three rows for a Facts list: what happened, what MailArkiv
  // does, and the next step.

  let { category }: { category: CollectionCategory } = $props();

  const words = $derived(CATEGORY_WORDS[category]);
</script>

<div>
  <dt>What happened</dt>
  <dd>{words.whatHappened}</dd>
</div>
<div>
  <dt>What MailArkiv does</dt>
  <dd>{words.whatMailArkivDoes}</dd>
</div>
<div>
  <dt>Next step</dt>
  <dd>{words.nextStep}</dd>
</div>
