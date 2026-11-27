import { spawn, type ChildProcess } from "node:child_process";
import { readFile } from "node:fs/promises";
import { request } from "node:https";
import { fileURLToPath } from "node:url";

// The image host stands in for the internet's image hosts. It serves a 1×1
// PNG at any /img/ path over HTTPS on 127.0.0.1, with a certificate it makes
// at start, and records every request it gets.

/**
 * The name messages use for the host. The browser maps it to 127.0.0.1 with
 * IMAGE_HOST_LAUNCH. Chromium treats loopback names and addresses as secure
 * and doesn't upgrade `http:` requests to them, so only a name that isn't
 * loopback behaves like an image host on the internet.
 */
export const IMAGE_HOST_NAME = "images.example.test";

/**
 * Browser launch options that map IMAGE_HOST_NAME to 127.0.0.1. Every other
 * name still goes to DNS, so leak.example.test fails to resolve. Only specs
 * that load from the host use them: with the mapping, Chromium takes 8
 * seconds to answer `route.fetch()`.
 */
export const IMAGE_HOST_LAUNCH = {
  args: [`--host-resolver-rules=MAP ${IMAGE_HOST_NAME} 127.0.0.1`],
};

const BINARY = fileURLToPath(
  new URL("../../../../bin/imagehost", import.meta.url),
);
const START_TIMEOUT_MS = 10_000;
const LOG_TIMEOUT_MS = 5_000;

/** What the host noted about one request. */
export interface HostRequest {
  method: string;
  path: string;
  /** Empty when the request carried none. */
  referer: string;
  cookie: boolean;
  /** Empty when the request carried none. */
  secFetchDest: string;
}

export interface ImageHost {
  /** Host and port, such as 127.0.0.1:41234. */
  address: string;
  /** The name and port messages use, such as images.example.test:41234. */
  origin: string;
  /** Every request so far, in the order they came. */
  requests(): Promise<HostRequest[]>;
  /** Reads a fixture template and fills `{{IMAGE_HOST}}` with `origin`. */
  fill(template: URL): Promise<Buffer>;
  stop(): Promise<void>;
}

export async function startImageHost(): Promise<ImageHost> {
  const child = spawn(BINARY, [], { stdio: ["ignore", "pipe", "pipe"] });
  let address: string;
  try {
    address = await readAddress(child);
  } catch (error) {
    await stopProcess(child);
    throw error;
  }
  const origin = `${IMAGE_HOST_NAME}:${new URL(`https://${address}`).port}`;
  return {
    address,
    origin,
    requests: () => readLog(address),
    fill: async (template) => {
      const text = await readFile(template, "latin1");
      return Buffer.from(text.replaceAll("{{IMAGE_HOST}}", origin), "latin1");
    },
    stop: () => stopProcess(child),
  };
}

/** The certificate is made at start, so only this reader skips checking it. */
function readLog(address: string): Promise<HostRequest[]> {
  return new Promise((resolve, reject) => {
    const call = request(
      `https://${address}/__log`,
      { rejectUnauthorized: false, timeout: LOG_TIMEOUT_MS },
      (response) => {
        let body = "";
        response.setEncoding("utf8");
        response.on("data", (chunk) => (body += chunk));
        response.on("end", () => {
          try {
            resolve(parseLog(JSON.parse(body)));
          } catch (error) {
            reject(error);
          }
        });
      },
    );
    call.on("timeout", () => call.destroy(new Error("the log didn't arrive")));
    call.on("error", reject);
    call.end();
  });
}

function parseLog(value: unknown): HostRequest[] {
  if (!Array.isArray(value)) throw new Error("the log isn't a list");
  return value.map((entry: Record<string, unknown>) => ({
    method: String(entry.method),
    path: String(entry.path),
    referer: String(entry.referer),
    cookie: entry.cookie === true,
    secFetchDest: String(entry.sec_fetch_dest),
  }));
}

/** Waits for the address the host prints on its first line. */
function readAddress(child: ChildProcess): Promise<string> {
  return new Promise((resolve, reject) => {
    let stdout = "";
    let stderr = "";
    const timer = setTimeout(
      () =>
        reject(
          new Error(`no address within ${START_TIMEOUT_MS} ms: ${stderr}`),
        ),
      START_TIMEOUT_MS,
    );
    child.stderr?.on("data", (chunk) => (stderr += chunk));
    child.stdout?.on("data", (chunk) => {
      stdout += chunk;
      const end = stdout.indexOf("\n");
      if (end === -1) return;
      clearTimeout(timer);
      resolve(stdout.slice(0, end));
    });
    child.on("error", (error) => {
      clearTimeout(timer);
      reject(error);
    });
    child.on("exit", (code) => {
      clearTimeout(timer);
      reject(new Error(`the image host exited with ${code}: ${stderr}`));
    });
  });
}

function stopProcess(child: ChildProcess): Promise<void> {
  if (child.exitCode !== null || child.signalCode !== null) {
    return Promise.resolve();
  }
  return new Promise((resolve) => {
    child.once("exit", () => resolve());
    child.kill("SIGTERM");
  });
}
