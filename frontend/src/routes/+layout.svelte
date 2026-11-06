<script lang="ts">
  import "@fontsource-variable/inter/wght.css";
  import "@fontsource-variable/geist-mono/wght.css";
  import "#lib/styles/tokens.css";
  import "#lib/styles/global.css";

  import type { LayoutProps } from "./$types";

  let { children }: LayoutProps = $props();
</script>

{@render children()}
