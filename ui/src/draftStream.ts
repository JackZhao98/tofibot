import { useSyncExternalStore } from "react";
import type { StreamDraft } from "./types";

export interface LiveDraft { content: string; revision: number; updated_at: string }

/**
 * The text of a streaming reply, kept outside React state. Every text delta used to be a state
 * change of the whole workspace, which re-rendered the entire timeline per chunk. The
 * `drafts` record in the workspace now changes only in structure (a draft appears, changes
 * owner or sequence, becomes visible, goes away); the growing text lives here and only the row
 * that shows it subscribes.
 */
export class DraftStream {
  private live = new Map<string, LiveDraft>();
  private published = new Map<string, StreamDraft>();
  private listeners = new Map<string, Set<() => void>>();

  get(id: string): LiveDraft | undefined { return this.live.get(id); }

  setLive(id: string, next: LiveDraft) {
    this.live.set(id, next);
    this.notify(id);
  }

  /** Aligns the live copies with a committed drafts record: new or replaced drafts take their content, absent ones are dropped. */
  reconcile(drafts: Record<string, StreamDraft>) {
    for (const [id, draft] of Object.entries(drafts)) {
      if (this.published.get(id) === draft) continue;
      this.published.set(id, draft);
      this.setLive(id, { content: draft.content, revision: draft.revision, updated_at: draft.updated_at });
    }
    for (const id of [...this.live.keys()]) {
      if (id in drafts) continue;
      this.live.delete(id);
      this.published.delete(id);
      this.notify(id);
    }
  }

  /** The drafts record with the newest text, for a snapshot that outlives the stream. */
  materialize(drafts: Record<string, StreamDraft>): Record<string, StreamDraft> {
    return Object.fromEntries(Object.entries(drafts).map(([id, draft]) => {
      const live = this.live.get(id);
      return [id, live ? { ...draft, content: live.content, revision: live.revision, updated_at: live.updated_at } : draft];
    }));
  }

  subscribe = (id: string, listener: () => void) => {
    let set = this.listeners.get(id);
    if (!set) this.listeners.set(id, set = new Set());
    set.add(listener);
    return () => { set!.delete(listener); if (!set!.size) this.listeners.delete(id); };
  };

  private notify(id: string) {
    for (const listener of [...(this.listeners.get(id) ?? [])]) listener();
  }
}

/** The live text of one draft, or undefined when `id` is absent or has no draft. */
export function useLiveDraft(stream: DraftStream, id: string | undefined): string | undefined {
  return useSyncExternalStore(
    listener => id ? stream.subscribe(id, listener) : () => {},
    () => id ? stream.get(id)?.content : undefined,
  );
}
