import { createServer, request } from "node:http";
import type { AddressInfo } from "node:net";

/**
 * A reverse proxy in front of the test server that can hold an import, the way
 * a slow network or a slow disk would. Everything else passes straight
 * through. A page's fetch stub can't show the storing state, because Chromium
 * finishes a stubbed upload only when its answer arrives.
 */
export interface ImportGate {
  /** The proxy's origin, also on localhost, so the session cookie works. */
  origin: string;
  /** How many imports have reached the proxy. */
  readonly imports: number;
  /** Holds the next imports: their bodies wait, then their answers wait. */
  hold(): void;
  /** Lets held bodies through to MailArkiv. */
  sendBody(): void;
  /** Lets MailArkiv's held answers through to the browser. */
  answer(): void;
  close(): Promise<void>;
}

export async function startImportGate(target: string): Promise<ImportGate> {
  const upstream = new URL(target);
  let imports = 0;
  let bodyGate = Promise.resolve();
  let answerGate = Promise.resolve();
  let openBody = () => {};
  let openAnswer = () => {};

  const proxy = createServer((incoming, outgoing) => {
    const isImport =
      incoming.method === "POST" && incoming.url === "/api/imports";
    const body = isImport ? bodyGate : Promise.resolve();
    const answer = isImport ? answerGate : Promise.resolve();
    if (isImport) {
      imports += 1;
      // Not reading fills the socket buffers, so the browser's upload stops
      // partway.
      incoming.pause();
    }

    const forward = request(
      {
        host: upstream.hostname,
        port: upstream.port,
        method: incoming.method,
        path: incoming.url,
        headers: incoming.headers,
      },
      (reply) => {
        void answer.then(() => {
          outgoing.writeHead(reply.statusCode ?? 502, reply.headers);
          reply.pipe(outgoing);
        });
      },
    );
    forward.on("error", () => outgoing.destroy());
    void body.then(() => incoming.pipe(forward));
  });

  await new Promise<void>((resolve) =>
    proxy.listen(0, "127.0.0.1", () => resolve()),
  );
  // A server listening on a TCP port always reports an AddressInfo.
  const { port } = proxy.address() as AddressInfo;

  return {
    origin: `http://localhost:${port}`,
    get imports() {
      return imports;
    },
    hold() {
      bodyGate = new Promise((resolve) => (openBody = resolve));
      answerGate = new Promise((resolve) => (openAnswer = resolve));
    },
    sendBody: () => openBody(),
    answer: () => openAnswer(),
    close: () =>
      new Promise<void>((resolve, reject) => {
        openBody();
        openAnswer();
        proxy.closeAllConnections();
        proxy.close((error) => (error ? reject(error) : resolve()));
      }),
  };
}
