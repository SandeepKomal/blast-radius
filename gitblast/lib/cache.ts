// A small in-memory TTL cache that also coalesces identical requests in
// flight, so a link shared in a busy channel triggers one analysis, not fifty.
// Each server instance has its own copy; the CDN in front absorbs the rest.

interface Entry<T> {
  value: T;
  expires: number;
}

export class TTLCache<T> {
  private entries = new Map<string, Entry<T>>();
  private pending = new Map<string, Promise<T>>();

  constructor(private max: number) {}

  async get(key: string, ttlMs: number, load: () => Promise<T>): Promise<T> {
    const hit = this.entries.get(key);
    if (hit && hit.expires > Date.now()) {
      // Refresh recency for LRU eviction.
      this.entries.delete(key);
      this.entries.set(key, hit);
      return hit.value;
    }
    const running = this.pending.get(key);
    if (running) return running;

    const p = load()
      .then((value) => {
        this.entries.set(key, { value, expires: Date.now() + ttlMs });
        while (this.entries.size > this.max) {
          const oldest = this.entries.keys().next().value;
          if (oldest === undefined) break;
          this.entries.delete(oldest);
        }
        return value;
      })
      .finally(() => this.pending.delete(key));
    this.pending.set(key, p);
    return p;
  }
}
