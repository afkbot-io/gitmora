/** Fences work when a query/project changes, including transports that ignore abort. */
export class LatestRequest {
  private generation = 0;
  private controller?: AbortController;
  begin(): { signal: AbortSignal; current: () => boolean } {
    this.cancel();
    const generation = this.generation;
    this.controller = new AbortController();
    return {
      signal: this.controller.signal,
      current: () => this.generation === generation,
    };
  }
  cancel(): void {
    this.generation++;
    this.controller?.abort();
  }
}
