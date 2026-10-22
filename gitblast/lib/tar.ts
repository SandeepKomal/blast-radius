// A minimal streaming reader for the .tar.gz archives GitHub serves. It keeps
// only the files the caller asks for and stops at hard limits, so a huge or
// hostile archive can't exhaust memory.

import { Readable } from "node:stream";
import { createGunzip } from "node:zlib";

export interface TarLimits {
  /** Bytes downloaded before giving up. */
  maxCompressed: number;
  /** Bytes after decompression before giving up (zip-bomb guard). */
  maxUnpacked: number;
  /** Number of entries before giving up. */
  maxEntries: number;
}

export interface TarFile {
  /** Path inside the repository, without GitHub's "owner-repo-sha/" prefix. */
  path: string;
  size: number;
  /** Present only when `want` returned true for this file. */
  content?: Buffer;
}

export class TarParser {
  private chunks: Buffer[] = [];
  private queued = 0;
  private state: "header" | "data" | "pad" | "done" = "header";
  private remaining = 0;
  private pad = 0;
  private type = "";
  private path = "";
  private size = 0;
  private body: Buffer[] | null = null;
  private longName: string | null = null;
  private paxPath: string | null = null;
  unpacked = 0;
  entries = 0;

  constructor(
    private want: (path: string, size: number) => boolean,
    private onFile: (f: TarFile) => void,
  ) {}

  get done(): boolean {
    return this.state === "done";
  }

  feed(chunk: Buffer): void {
    this.unpacked += chunk.length;
    this.chunks.push(chunk);
    this.queued += chunk.length;
    for (;;) {
      if (this.state === "done") return;
      if (this.state === "header") {
        if (this.queued < 512) return;
        this.header(this.take(512));
      } else if (this.state === "data") {
        if (this.queued === 0) return;
        const n = Math.min(this.remaining, this.queued);
        const part = this.take(n);
        if (this.body) this.body.push(part);
        this.remaining -= n;
        if (this.remaining === 0) this.finishEntry();
      } else if (this.state === "pad") {
        if (this.queued < this.pad) return;
        this.take(this.pad);
        this.state = "header";
      }
    }
  }

  private take(n: number): Buffer {
    if (n === 0) return Buffer.alloc(0);
    const first = this.chunks[0];
    if (first.length >= n) {
      this.chunks[0] = first.subarray(n);
      if (this.chunks[0].length === 0) this.chunks.shift();
      this.queued -= n;
      return first.subarray(0, n);
    }
    const out = Buffer.allocUnsafe(n);
    let off = 0;
    while (off < n) {
      const c = this.chunks[0];
      const k = Math.min(c.length, n - off);
      c.copy(out, off, 0, k);
      off += k;
      if (k === c.length) this.chunks.shift();
      else this.chunks[0] = c.subarray(k);
    }
    this.queued -= n;
    return out;
  }

  private header(h: Buffer): void {
    if (h.every((b) => b === 0)) {
      this.state = "done";
      return;
    }
    this.entries++;
    const name = cstr(h, 0, 100);
    const magic = h.toString("latin1", 257, 262);
    const prefix = magic === "ustar" ? cstr(h, 345, 155) : "";
    this.type = String.fromCharCode(h[156] || 48);
    this.size = parseSize(h.subarray(124, 136));
    let full = prefix ? `${prefix}/${name}` : name;
    if (this.paxPath !== null) full = this.paxPath;
    else if (this.longName !== null) full = this.longName;
    if (this.type !== "x" && this.type !== "g" && this.type !== "L") {
      this.paxPath = null;
      this.longName = null;
    }
    this.path = stripRoot(full);

    const meta = this.type === "x" || this.type === "L";
    const regular = this.type === "0" || this.type === "\0" || this.type === "7";
    this.body = meta || (regular && this.path !== "" && this.want(this.path, this.size)) ? [] : null;
    this.remaining = this.size;
    this.pad = (512 - (this.size % 512)) % 512;
    if (this.size === 0) this.finishEntry();
    else this.state = "data";
  }

  private finishEntry(): void {
    const body = this.body ? Buffer.concat(this.body) : undefined;
    if (this.type === "x" && body) {
      this.paxPath = paxValue(body, "path");
    } else if (this.type === "L" && body) {
      this.longName = body.toString("utf8").replace(/\0+$/, "");
    } else if ((this.type === "0" || this.type === "\0" || this.type === "7") && this.path) {
      this.onFile({ path: this.path, size: this.size, content: body });
    }
    this.body = null;
    this.state = this.pad > 0 ? "pad" : "header";
  }
}

function cstr(b: Buffer, off: number, len: number): string {
  const s = b.subarray(off, off + len);
  const end = s.indexOf(0);
  return s.toString("utf8", 0, end < 0 ? len : end);
}

function parseSize(field: Buffer): number {
  if (field[0] & 0x80) {
    // GNU base-256 encoding for files over 8 GiB.
    let n = 0;
    for (let i = 1; i < field.length; i++) n = n * 256 + field[i];
    return n;
  }
  const s = field.toString("latin1").replace(/[\0 ]+$/g, "").trim();
  return s ? parseInt(s, 8) : 0;
}

function paxValue(body: Buffer, key: string): string | null {
  let off = 0;
  while (off < body.length) {
    const space = body.indexOf(0x20, off);
    if (space < 0) break;
    const len = parseInt(body.toString("latin1", off, space), 10);
    if (!len) break;
    const rec = body.toString("utf8", space + 1, off + len - 1);
    const eq = rec.indexOf("=");
    if (rec.slice(0, eq) === key) return rec.slice(eq + 1);
    off += len;
  }
  return null;
}

/** GitHub wraps everything in "owner-repo-sha/"; drop that first component. */
function stripRoot(p: string): string {
  const i = p.indexOf("/");
  return i < 0 ? "" : p.slice(i + 1);
}

/**
 * Reads a gzipped tar stream. Returns `truncated: true` if a limit was hit;
 * the files seen up to that point were already delivered to `onFile`.
 */
export async function readTarGz(
  stream: ReadableStream<Uint8Array>,
  limits: TarLimits,
  want: (path: string, size: number) => boolean,
  onFile: (f: TarFile) => void,
): Promise<{ truncated: boolean }> {
  const source = Readable.fromWeb(stream as import("node:stream/web").ReadableStream<Uint8Array>);
  const gunzip = createGunzip();
  let compressed = 0;
  let truncated = false;
  source.on("data", (c: Buffer) => {
    compressed += c.length;
    if (compressed > limits.maxCompressed) {
      truncated = true;
      source.destroy();
      gunzip.end();
    }
  });
  source.on("error", () => gunzip.end());
  source.pipe(gunzip);

  const parser = new TarParser(want, onFile);
  try {
    for await (const chunk of gunzip as AsyncIterable<Buffer>) {
      parser.feed(chunk);
      if (parser.done) break;
      if (parser.unpacked > limits.maxUnpacked || parser.entries > limits.maxEntries) {
        truncated = true;
        break;
      }
    }
  } catch {
    // A cut-off download leaves a partial archive; keep what was read.
    truncated = true;
  } finally {
    source.destroy();
    gunzip.destroy();
  }
  return { truncated };
}
