import { spawn, type ChildProcess } from "node:child_process";
import { mkdtemp, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const BINARY = fileURLToPath(
  new URL("../../../../bin/mailarkiv", import.meta.url),
);
const START_TIMEOUT_MS = 10_000;
const POLL_INTERVAL_MS = 50;

export interface TestServer {
  /** Uses localhost, the host research tested with the Secure session cookie. */
  origin: string;
  /** The server's data directory, removed by stop. */
  dataDir: string;
  /** Creates an account through the CLI, as an operator would. */
  createAccount(email: string, password: string): Promise<void>;
  /** Sets a new password through the CLI, which ends the account's sessions. */
  setPassword(email: string, password: string): Promise<void>;
  /**
   * Stops the server with SIGTERM and starts it again on the same port and
   * data directory, so the origin and the session cookie stay the same.
   */
  restart(): Promise<void>;
  stop(): Promise<void>;
}

/** Starts the built binary on a free port with a new data directory. */
export async function startServer(): Promise<TestServer> {
  const dataDir = await mkdtemp(join(tmpdir(), "mailarkiv-e2e-"));
  const port = await freePort();
  const origin = `http://localhost:${port}`;

  let child: ChildProcess;
  try {
    child = await launch(dataDir, port, origin);
  } catch (error) {
    await rm(dataDir, { recursive: true, force: true });
    throw error;
  }

  return {
    origin,
    dataDir,
    createAccount: (email, password) =>
      runAccountCommand("create", dataDir, email, password),
    setPassword: (email, password) =>
      runAccountCommand("set-password", dataDir, email, password),
    restart: async () => {
      await stopProcess(child);
      child = await launch(dataDir, port, origin);
    },
    stop: async () => {
      await stopProcess(child);
      await rm(dataDir, { recursive: true, force: true });
    },
  };
}

/** Runs the server and returns once it answers. */
async function launch(
  dataDir: string,
  port: number,
  origin: string,
): Promise<ChildProcess> {
  const child = spawn(
    BINARY,
    ["serve", "--data-dir", dataDir, "--listen", `127.0.0.1:${port}`],
    { stdio: ["ignore", "ignore", "pipe"] },
  );

  let stderr = "";
  child.stderr?.on("data", (chunk) => (stderr += chunk));

  try {
    await waitUntilAnswering(origin, child);
  } catch (error) {
    await stopProcess(child);
    throw new Error(`The server didn't start: ${String(error)}\n${stderr}`);
  }
  return child;
}

/**
 * Runs an `account` command. stdin is a pipe, not a terminal, so the command
 * reads the password as one line.
 */
function runAccountCommand(
  command: "create" | "set-password",
  dataDir: string,
  email: string,
  password: string,
): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(
      BINARY,
      ["account", command, "--data-dir", dataDir, email],
      { stdio: ["pipe", "ignore", "pipe"] },
    );
    let stderr = "";
    child.stderr?.on("data", (chunk) => (stderr += chunk));
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(new Error(`account ${command} exited with ${code}: ${stderr}`));
      }
    });
    child.stdin?.end(`${password}\n`);
  });
}

function freePort(): Promise<number> {
  return new Promise((resolve, reject) => {
    const probe = createServer();
    probe.on("error", reject);
    probe.listen(0, "127.0.0.1", () => {
      const address = probe.address();
      if (address === null || typeof address === "string") {
        reject(new Error(`Unexpected probe address: ${address}`));
        return;
      }
      probe.close(() => resolve(address.port));
    });
  });
}

async function waitUntilAnswering(
  origin: string,
  child: ChildProcess,
): Promise<void> {
  const deadline = Date.now() + START_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`it exited with code ${child.exitCode}`);
    }
    try {
      await fetch(origin, {
        signal: AbortSignal.timeout(POLL_INTERVAL_MS * 4),
      });
      return;
    } catch {
      // Not listening yet. The deadline above ends the wait.
      await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
    }
  }
  throw new Error(`no answer within ${START_TIMEOUT_MS} ms`);
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
