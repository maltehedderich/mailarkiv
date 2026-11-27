import { spawn, type ChildProcess } from "node:child_process";
import { copyFile, mkdtemp, readFile, readdir, rm } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { fileURLToPath } from "node:url";

const BINARY = fileURLToPath(
  new URL("../../../../bin/mailarkiv", import.meta.url),
);
const FAULTS_SEARCH = fileURLToPath(
  new URL("../../../../bin/faults/mailarkiv-search", import.meta.url),
);
const START_TIMEOUT_MS = 10_000;
const POLL_INTERVAL_MS = 50;

export interface ServerOptions {
  /**
   * Creates a key outside the data directory, starts the server with it,
   * and makes the server trust only the CA in this file.
   */
  collection?: { caFile: string };
  /**
   * Runs a copy of the server from its own directory, because the server
   * runs the search program it finds beside itself. There the program is
   * missing, or is the build with fault points, given this
   * `MAILARKIV_SEARCH_FAULTS` list.
   */
  search?: "missing" | { faults: string };
}

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
   * A server started with search faults can get a new fault list, which the
   * next start's first search process gets.
   */
  restart(options?: { searchFaults?: string }): Promise<void>;
  /** Kills the server's search process with SIGKILL, as a crash would. */
  killSearch(): Promise<void>;
  stop(): Promise<void>;
}

/** Starts the built binary on a free port with a new data directory. */
export async function startServer(
  options: ServerOptions = {},
): Promise<TestServer> {
  const dataDir = await mkdtemp(join(tmpdir(), "mailarkiv-e2e-"));
  const keyDir =
    options.collection === undefined
      ? null
      : await mkdtemp(join(tmpdir(), "mailarkiv-e2e-key-"));
  const binDir =
    options.search === undefined
      ? null
      : await mkdtemp(join(tmpdir(), "mailarkiv-e2e-bin-"));
  const port = await freePort();
  const origin = `http://localhost:${port}`;
  const removeDirs = () =>
    Promise.all(
      [dataDir, keyDir, binDir]
        .filter((dir) => dir !== null)
        .map((dir) => rm(dir, { recursive: true, force: true })),
    );

  let collection: Collection | null = null;
  let program: Program = { binary: BINARY, env: {}, search: true };
  let child: ChildProcess;
  try {
    if (options.search !== undefined && binDir !== null) {
      program = {
        binary: join(binDir, "mailarkiv"),
        env: {},
        search: options.search !== "missing",
      };
      await copyFile(BINARY, program.binary);
      if (options.search !== "missing") {
        await copyFile(FAULTS_SEARCH, join(binDir, "mailarkiv-search"));
        program.env.MAILARKIV_SEARCH_FAULTS = options.search.faults;
      }
    }
    if (options.collection !== undefined && keyDir !== null) {
      const keyFile = join(keyDir, "key");
      await runCommand(["key", "create", "--data-dir", dataDir, keyFile]);
      collection = { keyFile, caFile: options.collection.caFile };
    }
    child = await launch(program, dataDir, port, origin, collection);
  } catch (error) {
    await removeDirs();
    throw error;
  }

  return {
    origin,
    dataDir,
    createAccount: (email, password) =>
      runAccountCommand("create", dataDir, email, password),
    setPassword: (email, password) =>
      runAccountCommand("set-password", dataDir, email, password),
    restart: async ({ searchFaults } = {}) => {
      await stopProcess(child);
      if (searchFaults !== undefined) {
        if (program.env.MAILARKIV_SEARCH_FAULTS === undefined) {
          throw new Error("search faults need a server started with them");
        }
        program.env.MAILARKIV_SEARCH_FAULTS = searchFaults;
      }
      child = await launch(program, dataDir, port, origin, collection);
    },
    killSearch: async () => {
      const pid = await searchProcess(child);
      process.kill(pid, "SIGKILL");
    },
    stop: async () => {
      await stopProcess(child);
      await removeDirs();
    },
  };
}

/** The server binary to run, and what it adds to the environment. */
interface Program {
  binary: string;
  env: Record<string, string>;
  /** Whether a search program sits beside the binary. */
  search: boolean;
}

/** What the server needs to collect mail. */
interface Collection {
  keyFile: string;
  caFile: string;
}

/** Runs the server and returns once it answers. */
async function launch(
  program: Program,
  dataDir: string,
  port: number,
  origin: string,
  collection: Collection | null,
): Promise<ChildProcess> {
  const args = [
    "serve",
    "--data-dir",
    dataDir,
    "--listen",
    `127.0.0.1:${port}`,
  ];
  // The server reads SSL_CERT_FILE in place of the host's trusted
  // certificates, so it trusts the test mail server and nothing else.
  let env = { ...process.env, ...program.env };
  if (collection !== null) {
    args.push("--key-file", collection.keyFile);
    env = { ...env, SSL_CERT_FILE: collection.caFile };
  }
  const child = spawn(program.binary, args, {
    env,
    stdio: ["ignore", "ignore", "pipe"],
  });

  let stderr = "";
  child.stderr?.on("data", (chunk) => (stderr += chunk));

  try {
    await waitUntilAnswering(origin, child);
    // Search counts as a problem until its process has started, so the
    // problem count a test reads right after the start would depend on
    // timing.
    if (program.search) await waitUntilSearchStarted(() => stderr, child);
  } catch (error) {
    await stopProcess(child);
    throw new Error(`The server didn't start: ${String(error)}\n${stderr}`);
  }
  return child;
}

/**
 * Waits for the first search state line that isn't Unavailable: Available,
 * or Needs rebuild when the index can't be used.
 */
async function waitUntilSearchStarted(
  stderr: () => string,
  child: ChildProcess,
): Promise<void> {
  const deadline = Date.now() + START_TIMEOUT_MS;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) {
      throw new Error(`it exited with code ${child.exitCode}`);
    }
    // The last element is a line still being written, or empty.
    for (const line of stderr().split("\n").slice(0, -1)) {
      const entry = JSON.parse(line) as { msg?: string; state?: string };
      if (entry.msg === "search state" && entry.state !== "unavailable") {
        return;
      }
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
  }
  throw new Error(`search didn't start within ${START_TIMEOUT_MS} ms`);
}

/** The server's search process, read from its children in /proc. */
async function searchProcess(child: ChildProcess): Promise<number> {
  const deadline = Date.now() + START_TIMEOUT_MS;
  while (Date.now() < deadline) {
    const tasks = `/proc/${child.pid}/task`;
    for (const task of await readdir(tasks)) {
      const children = await readFile(`${tasks}/${task}/children`, "utf8");
      for (const pid of children.split(" ").filter(Boolean)) {
        const command = await readFile(`/proc/${pid}/cmdline`, "utf8").catch(
          () => "",
        );
        if (command.split("\0")[0].endsWith("/mailarkiv-search")) {
          return Number(pid);
        }
      }
    }
    await new Promise((resolve) => setTimeout(resolve, POLL_INTERVAL_MS));
  }
  throw new Error(`no search process within ${START_TIMEOUT_MS} ms`);
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
  return runCommand(
    ["account", command, "--data-dir", dataDir, email],
    `${password}\n`,
  );
}

/** Runs the binary with args, writing input to its stdin, and expects exit 0. */
function runCommand(args: string[], input = ""): Promise<void> {
  return new Promise((resolve, reject) => {
    const child = spawn(BINARY, args, { stdio: ["pipe", "ignore", "pipe"] });
    let stderr = "";
    child.stderr?.on("data", (chunk) => (stderr += chunk));
    child.on("error", reject);
    child.on("exit", (code) => {
      if (code === 0) {
        resolve();
      } else {
        reject(
          new Error(
            `${args.slice(0, 2).join(" ")} exited with ${code}: ${stderr}`,
          ),
        );
      }
    });
    child.stdin?.end(input);
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
