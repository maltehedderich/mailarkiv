import { MARKER } from "#lib/api.js";

import { classify, type Classified, type ImportResponse } from "./classify";

/**
 * The upload gives up after this long with no progress, or this long after
 * the last byte left with no answer. Either way the result is Unconfirmed.
 */
export const STALL_MS = 120_000;

export interface UploadCallbacks {
  /** The share of the file sent so far, from 0 to 1. */
  onProgress?: (fraction: number) => void;
  /** Every byte has left. The server is storing the file. */
  onUploaded?: () => void;
}

/**
 * Sends one file to POST /api/imports. XMLHttpRequest is used because fetch
 * reports no upload progress (FR-035).
 */
export function upload(
  file: File,
  { onProgress, onUploaded }: UploadCallbacks = {},
): Promise<Classified> {
  return new Promise((resolve) => {
    const xhr = new XMLHttpRequest();
    let timer: ReturnType<typeof setTimeout> | undefined;
    let settled = false;

    const finish = (response: ImportResponse) => {
      if (settled) return;
      settled = true;
      clearTimeout(timer);
      resolve(classify(response));
    };
    const wait = () => {
      clearTimeout(timer);
      timer = setTimeout(() => {
        finish({ kind: "timeout" });
        xhr.abort();
      }, STALL_MS);
    };

    xhr.upload.addEventListener("progress", (event) => {
      wait();
      if (event.lengthComputable && event.total > 0) {
        onProgress?.(event.loaded / event.total);
      }
    });
    xhr.upload.addEventListener("load", () => {
      wait();
      onUploaded?.();
    });
    xhr.addEventListener("load", () => {
      finish({
        kind: "response",
        status: xhr.status,
        marked: xhr.getResponseHeader(MARKER) === "1",
        body: xhr.responseText,
      });
    });
    xhr.addEventListener("error", () => finish({ kind: "network_error" }));
    xhr.addEventListener("abort", () => finish({ kind: "aborted" }));

    xhr.open("POST", "/api/imports");
    xhr.setRequestHeader("MailArkiv-File-Name", encodeURIComponent(file.name));
    xhr.send(file);
    wait();
  });
}
