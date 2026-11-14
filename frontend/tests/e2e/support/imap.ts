import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const BINARY = fileURLToPath(
  new URL("../../../../bin/imaptestserver", import.meta.url),
);
const START_TIMEOUT_MS = 10_000;

export interface ImapFolder {
  name: string;
  /** How many synthetic messages it holds, each with different bytes. */
  messages: number;
}

export interface ImapOptions {
  user: string;
  password: string;
  /** In the order the server lists them, after INBOX. */
  folders: ImapFolder[];
  /** How long each message's first body fetch waits, so a sync can be watched. */
  fetchDelayMs?: number;
}

export interface ImapServer {
  /** The certificate names 127.0.0.1, so this is the host to type. */
  host: string;
  port: number;
  /** The test CA, for the MailArkiv server's SSL_CERT_FILE. */
  caFile: string;
  stop(): Promise<void>;
}

/**
 * Starts the test IMAP server over implicit TLS on a port the system picks,
 * with one mailbox. It prints its address once the mailbox is filled.
 */
export async function startImap(options: ImapOptions): Promise<ImapServer> {
  const dir = await mkdtemp(join(tmpdir(), "mailarkiv-e2e-imap-"));
  const caFile = join(dir, "ca.pem");
  const args = [
    "--ca-out",
    caFile,
    "--user",
    options.user,
    "--password",
    options.password,
  ];
  for (const folder of options.folders) {
    args.push("--folder", `${folder.name}=${folder.messages}`);
  }
  if (options.fetchDelayMs !== undefined) {
    args.push("--fetch-delay", `${options.fetchDelayMs}ms`);
  }

  const child = spawn(BINARY, args, { stdio: ["ignore", "pipe", "pipe"] });
  let address: string;
  try {
    address = await readAddress(child);
  } catch (error) {
    await stopProcess(child);
    await rm(dir, { recursive: true, force: true });
    throw error;
  }

  const at = address.lastIndexOf(":");
  return {
    host: address.slice(0, at),
    port: Number(address.slice(at + 1)),
    caFile,
    stop: async () => {
      await stopProcess(child);
      await rm(dir, { recursive: true, force: true });
    },
  };
}

/** Waits for the address line the server prints once it's ready. */
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
      reject(new Error(`the IMAP server exited with ${code}: ${stderr}`));
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
