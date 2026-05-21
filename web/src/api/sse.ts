// Server-Sent Events helper. Used for two backend patterns:
//
//   1. POST + SSE on the same response — /clusters/import/discover.
//      EventSource doesn't support POST, so we use fetch + ReadableStream.
//   2. GET SSE — /operations/:taskId/events. Could use EventSource, but
//      AbortController + custom event names + Last-Event-ID handling are
//      cleaner with fetch.
//
// The low-level sseStream() yields parsed frames; high-level wrappers
// (subscribeOperationEvents, discoverClusterStream) decode the
// per-endpoint payload into typed callbacks.

import type {
  DiscoveryProgress,
  ImportCandidate,
  ImportError,
  OperationProgress,
  OpEvent,
} from "./types";

const BASE = "/api/v1";

// SseError is thrown when the initial response is non-2xx (the connection
// never establishes). Once the stream is open, partial reads and aborts
// surface as DOMException("AbortError") or via the iterator returning.
export class SseError extends Error {
  status: number;
  body: string;
  constructor(status: number, body: string) {
    super(`SSE ${status}: ${body || "(empty)"}`);
    this.name = "SseError";
    this.status = status;
    this.body = body;
  }
}

// SseFrame is one decoded event-stream record.
export interface SseFrame {
  id?: string;
  event?: string; // defaults to "message" when omitted
  data: string;
}

// sseStream connects to url with init (custom method/headers/body welcome)
// and yields decoded frames until the server closes the stream or the
// caller's AbortSignal fires.
export async function* sseStream(
  url: string,
  init: RequestInit = {},
): AsyncGenerator<SseFrame, void, void> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "text/event-stream");
  // Help any proxies see the long-lived intent.
  headers.set("Cache-Control", "no-cache");

  const resp = await fetch(url, { ...init, headers });
  if (!resp.ok || !resp.body) {
    const body = await safeText(resp);
    throw new SseError(resp.status, body);
  }

  const reader = resp.body
    .pipeThrough(new TextDecoderStream("utf-8"))
    .getReader();

  let buf = "";
  try {
    while (true) {
      const { done, value } = await reader.read();
      if (done) return;
      buf += value;

      // SSE frames are separated by a blank line. Both `\n\n` and
      // `\r\n\r\n` are legal; normalize CRLF to LF for parsing.
      buf = buf.replace(/\r\n/g, "\n");
      let idx;
      while ((idx = buf.indexOf("\n\n")) >= 0) {
        const raw = buf.slice(0, idx);
        buf = buf.slice(idx + 2);
        const frame = parseFrame(raw);
        if (frame) yield frame;
      }
    }
  } finally {
    try {
      reader.releaseLock();
    } catch {
      // Lock release can throw if the stream errored — safe to swallow.
    }
  }
}

// parseFrame turns a raw SSE block into a frame, or null for keepalive
// comments and frames with no data field.
function parseFrame(raw: string): SseFrame | null {
  let id: string | undefined;
  let event: string | undefined;
  const dataLines: string[] = [];
  for (const line of raw.split("\n")) {
    if (line === "" || line.startsWith(":")) continue;
    const colon = line.indexOf(":");
    const field = colon >= 0 ? line.slice(0, colon) : line;
    let value = colon >= 0 ? line.slice(colon + 1) : "";
    if (value.startsWith(" ")) value = value.slice(1);
    switch (field) {
      case "id":
        id = value;
        break;
      case "event":
        event = value;
        break;
      case "data":
        dataLines.push(value);
        break;
      default:
        // unknown field — spec says ignore.
        break;
    }
  }
  if (dataLines.length === 0) return null;
  return { id, event, data: dataLines.join("\n") };
}

async function safeText(resp: Response): Promise<string> {
  try {
    return await resp.text();
  } catch {
    return "";
  }
}

// ---- high-level wrappers ----

// DiscoverHandlers gets a callback for each progress line and a final
// resolve when the result frame arrives. Errors from the result frame
// are surfaced via onError; transport errors throw from the returned
// promise.
export interface DiscoverHandlers {
  onProgress: (line: DiscoveryProgress) => void;
  signal?: AbortSignal;
}

export type DiscoverResult =
  | { ok: true; candidate: ImportCandidate }
  | { ok: false; error: ImportError };

// discoverClusterStream POSTs creds and yields progress over SSE on the
// same response. Returns the final result frame's body. The backend
// guarantees exactly one `event: result` frame and closes the stream.
export async function discoverClusterStream(
  body: { url: string; username: string; password: string },
  handlers: DiscoverHandlers,
): Promise<DiscoverResult> {
  const stream = sseStream(`${BASE}/clusters/import/discover`, {
    method: "POST",
    headers: { "Content-Type": "application/json" },
    body: JSON.stringify(body),
    signal: handlers.signal,
  });
  let result: DiscoverResult | null = null;
  for await (const frame of stream) {
    if (frame.event === "log") {
      try {
        const payload = JSON.parse(frame.data) as DiscoveryProgress;
        handlers.onProgress(payload);
      } catch {
        // Drop malformed log frames rather than abort the stream.
      }
      continue;
    }
    if (frame.event === "result") {
      try {
        result = JSON.parse(frame.data) as DiscoverResult;
      } catch (e) {
        throw new SseError(0, `bad result frame: ${frame.data}`);
      }
      // The server keeps the connection open briefly after `result` so
      // late readers get the frame. We have what we need — break out.
      break;
    }
    // event: end / unknown — ignore; the stream will close on its own.
  }
  if (!result) {
    throw new SseError(0, "stream closed without result frame");
  }
  return result;
}

// OperationHandlers is the contract for operation event subscriptions.
// onProgress fires on every state frame (the backend pushes one on
// connect with the current snapshot, then incrementally as the op runs).
// onEvent fires for log frames (one per OpEvent published by the
// executor). The promise resolves when the stream closes (terminal
// state), or rejects on transport / abort errors.
export interface OperationHandlers {
  onProgress: (progress: OperationProgress) => void;
  onEvent?: (ev: OpEvent) => void;
  signal?: AbortSignal;
}

export async function subscribeOperationEvents(
  taskId: string,
  handlers: OperationHandlers,
): Promise<void> {
  const stream = sseStream(
    `${BASE}/operations/${encodeURIComponent(taskId)}/events`,
    { signal: handlers.signal },
  );
  for await (const frame of stream) {
    if (frame.event === "state") {
      try {
        const progress = JSON.parse(frame.data) as OperationProgress;
        handlers.onProgress(progress);
      } catch {
        // Ignore; the next frame will catch up.
      }
      continue;
    }
    if (frame.event === "log") {
      if (!handlers.onEvent) continue;
      try {
        const ev = JSON.parse(frame.data) as OpEvent;
        handlers.onEvent(ev);
      } catch {
        // ignore
      }
      continue;
    }
    if (frame.event === "end") {
      return;
    }
  }
}

// subscribeNodeLogs and subscribeNodeTrace target the M3-future SSE
// endpoints (currently 501). Shape matches subscribeOperationEvents so
// callers can swap them in without rewriting the consumer.
//
// The backend stub returns 501 with a `not_implemented` envelope; this
// helper surfaces that as a thrown SseError(501, ...) the caller can
// branch on to render a "not yet wired" placeholder.
export async function subscribeNodeLogs(
  clusterId: string,
  nodeId: string,
  onLine: (line: { ts: string; text: string; level?: string }) => void,
  signal?: AbortSignal,
): Promise<void> {
  const stream = sseStream(
    `${BASE}/clusters/${encodeURIComponent(clusterId)}/nodes/${encodeURIComponent(nodeId)}/logs`,
    { signal },
  );
  for await (const frame of stream) {
    if (frame.event === "log" || frame.event === "message" || !frame.event) {
      try {
        onLine(JSON.parse(frame.data));
      } catch {
        onLine({ ts: new Date().toISOString(), text: frame.data });
      }
    }
  }
}

export async function subscribeNodeTrace(
  clusterId: string,
  nodeId: string,
  onLine: (line: { ts: string; text: string; level?: string }) => void,
  signal?: AbortSignal,
): Promise<void> {
  const stream = sseStream(
    `${BASE}/clusters/${encodeURIComponent(clusterId)}/nodes/${encodeURIComponent(nodeId)}/trace`,
    { signal },
  );
  for await (const frame of stream) {
    if (frame.event === "log" || frame.event === "message" || !frame.event) {
      try {
        onLine(JSON.parse(frame.data));
      } catch {
        onLine({ ts: new Date().toISOString(), text: frame.data });
      }
    }
  }
}
