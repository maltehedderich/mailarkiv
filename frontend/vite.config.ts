import adapter from "@sveltejs/adapter-static";
import { sveltekit } from "@sveltejs/kit/vite";
import Icons from "unplugin-icons/vite";
import { defineConfig } from "vitest/config";

export default defineConfig({
  plugins: [
    sveltekit({
      adapter: adapter({ fallback: "app.html" }),
      // Absolute asset links, so app.html works at every path the server
      // sends it for. MailArkiv must be served at the root of its host name.
      paths: { relative: false },
      // Kit writes this policy to a <meta> tag, which can't carry
      // frame-ancestors. The server sends that one as a response header.
      csp: {
        mode: "hash",
        directives: {
          "default-src": ["self"],
          "script-src": ["self"],
          "style-src": ["self"],
          "style-src-attr": ["unsafe-inline"],
          "object-src": ["none"],
          "base-uri": ["none"],
          "form-action": ["self"],
        },
      },
    }),
    Icons({
      compiler: "svelte",
      scale: 1,
      defaultClass: "icon",
      autoInstall: false,
      iconCustomizer: (_collection, _icon, props) => {
        props["aria-hidden"] = "true";
      },
    }),
  ],
  server: {
    proxy: { "/api": "http://127.0.0.1:8080" },
  },
  test: {
    include: ["src/**/*.test.ts"],
  },
});
