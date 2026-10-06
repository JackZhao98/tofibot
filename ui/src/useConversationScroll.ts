import { desktopState, updateDesktopState } from "./desktop";
import { useCallback, useLayoutEffect, useRef, useState } from "react";
import type { Conversation, Message } from "./types";

interface Options {
  conversation?: Conversation;
  messages: Message[];
  ready: boolean;
  hasMore: boolean;
  loadingOlder: boolean;
  mobileList: boolean;
  loadOlder: () => Promise<boolean>;
  markRead: (conversationId: string, seq: number) => void;
}

interface Visit {
  id: string;
  readSeq: number;
  entryMax: number | null;
  resume: ViewSnapshot | null;
  positioned: boolean;
  atBottom: boolean;
  nearBottom: boolean;
  width: number;
  height: number;
  baseline: number;
  detached: boolean;
  resumeIntent: boolean;
  lastScrollTop: number;
  prepend: { top: number; height: number; anchorSeq: number; anchorOffset: number } | null;
  followNext: boolean;
  loadingBoundary: boolean;
  boundaryFailed: boolean;
  hold: { node: HTMLElement; offset: number; until: number } | null;
}

// A disclosure the reader opens grows the page under their pointer; follow-bottom
// must not chase that growth. Hold the clicked row in place while it settles.
const HOLD_MS = 900;

interface ViewSnapshot {
  anchorSeq: number;
  anchorOffset: number;
  entryMax: number | null;
  atBottom: boolean;
  detached: boolean;
}

function captureViewSnapshot(el: HTMLDivElement, visit: Visit): ViewSnapshot {
  const containerTop = el.getBoundingClientRect().top;
  const nodes = Array.from(el.querySelectorAll<HTMLElement>("[data-message-seq]"));
  const node = nodes.find(candidate => candidate.getBoundingClientRect().bottom > containerTop + 1) ?? nodes[nodes.length - 1];
  const anchorSeq = node ? Number(node.dataset.messageSeq ?? 0) : 0;
  return {
    anchorSeq: Number.isFinite(anchorSeq) ? anchorSeq : 0,
    anchorOffset: node ? node.getBoundingClientRect().top - containerTop : 0,
    entryMax: visit.entryMax,
    atBottom: visit.atBottom,
    detached: visit.detached,
  };
}

export function useConversationScroll(options: Options) {
  const scrollRef = useRef<HTMLDivElement>(null);
  const latest = useRef(options);
  latest.current = options;
  const visit = useRef<Visit | null>(null);
  const snapshots = useRef(new Map<string, ViewSnapshot>(Object.entries(desktopState().readingPositions ?? {}).filter((entry): entry is [string, ViewSnapshot] => { const value = entry[1] as ViewSnapshot; return Boolean(value && Number.isFinite(value.anchorSeq) && Number.isFinite(value.anchorOffset) && typeof value.atBottom === "boolean"); })));
  const [, render] = useState(0);
  const id = options.conversation?.id ?? "";

  const rememberVisit = (candidate: Visit | null) => {
    const el = scrollRef.current;
    if (!el || !candidate?.positioned || !candidate.id) return;
    snapshots.current.set(candidate.id, captureViewSnapshot(el, candidate));
    updateDesktopState({ readingPositions: Object.fromEntries([...snapshots.current.entries()].slice(-100)) });
  };

  if (visit.current?.id !== id) {
    rememberVisit(visit.current);
    const snapshot = id ? snapshots.current.get(id) ?? null : null;
    visit.current = { id, readSeq: options.conversation?.read_seq ?? 0, entryMax: snapshot?.entryMax ?? null,
      resume: snapshot, positioned: false, atBottom: snapshot?.atBottom ?? true, nearBottom: true, width: 0, height: 0,
      baseline: 0, detached: snapshot?.detached ?? false, resumeIntent: false, lastScrollTop: 0,
      prepend: null, followNext: false, loadingBoundary: false, boundaryFailed: false, hold: null };
  }

  const isVisible = useCallback(() => document.visibilityState === "visible" &&
    (!latest.current.mobileList || window.matchMedia("(min-width: 768px)").matches), []);

  const markVisible = useCallback(() => {
    const v = visit.current;
    const o = latest.current;
    if (!v?.positioned || !v.atBottom || !o.ready || !isVisible()) return;
    const seq = o.messages.reduce((max, m) => m.conversation_id === v.id ? Math.max(max, m.seq) : max, 0);
    if (seq) o.markRead(v.id, seq);
  }, [isVisible]);

  const syncPosition = useCallback((fromUserScroll = false) => {
    const el = scrollRef.current;
    const v = visit.current;
    if (!el || !v?.positioned) return;
    if (v.atBottom && (v.width !== el.clientWidth || v.height !== el.clientHeight)) el.scrollTop = el.scrollHeight;
    v.width = el.clientWidth;
    v.height = el.clientHeight;
    const distanceFromBottom = el.scrollHeight - el.clientHeight - el.scrollTop;
    const bottom = distanceFromBottom < 48;
    // The near-bottom threshold is useful for displaying state, but it cannot
    // cancel an explicit upward gesture. Only an actual return to the bottom
    // may re-enable streaming follow. Allow fractional CSS-pixel rounding at
    // the physical bottom, including a clamped resize/keyboard scroll event.
    const returnedToBottom = distanceFromBottom <= 2;
    if (fromUserScroll && v.detached && returnedToBottom) v.detached = false;
    v.resumeIntent = false;
    v.lastScrollTop = el.scrollTop;
    const following = !v.detached && bottom;
    const changed = v.atBottom !== following || v.nearBottom !== bottom;
    v.nearBottom = bottom;
    v.atBottom = following;
    if (following) {
      const seq = latest.current.messages.reduce((max, m) => m.conversation_id === v.id ? Math.max(max, m.seq) : max, 0);
      if (v.baseline !== seq) { v.baseline = seq; render(n => n + 1); }
      markVisible();
    }
    if (changed) render(n => n + 1);
    rememberVisit(v);
  }, [markVisible]);

  const position = useCallback(() => {
    const el = scrollRef.current;
    const v = visit.current;
    const o = latest.current;
    if (!el || !v || !o.ready || !isVisible()) return;
    const records = o.messages.filter(m => m.conversation_id === v.id);
    const min = records[0]?.seq ?? 0;
    const max = records[records.length - 1]?.seq ?? 0;
    if (v.entryMax === null) v.entryMax = max;
    if (!v.positioned) {
      const resume = v.resume;
      const resumeNode = resume?.anchorSeq ? el.querySelector<HTMLElement>(`[data-message-seq="${resume.anchorSeq}"]`) : null;
      if (resume && !resume.atBottom && resume.anchorSeq > 0 && !resumeNode) {
        if (v.boundaryFailed) return;
        if (min > resume.anchorSeq && o.hasMore && !o.loadingOlder && !v.loadingBoundary) {
          v.loadingBoundary = true;
          void o.loadOlder().then(ok => {
            if (visit.current !== v) return;
            v.loadingBoundary = false;
            v.boundaryFailed = !ok;
            render(n => n + 1);
          });
        }
        // Keep the restore intent until the anchor is actually in the DOM.
        // This also covers the transient empty page while a switched
        // conversation is loading and prevents an accidental jump to bottom.
        if (o.hasMore || o.loadingOlder || records.length === 0) return;
      }
      // Fetch the actual unread boundary before positioning or acknowledging it.
      if (!resume && !v.followNext && max > v.readSeq && min > v.readSeq + 1 && o.hasMore) {
        if (v.boundaryFailed) return;
        if (!o.loadingOlder && !v.loadingBoundary) {
          v.loadingBoundary = true;
          void o.loadOlder().then(ok => {
            if (visit.current !== v) return;
            v.loadingBoundary = false;
            v.boundaryFailed = !ok;
            render(n => n + 1);
          });
        }
        return;
      }
      const first = resume ? undefined : (v.followNext ? undefined : records.find(m => m.seq > v.readSeq));
      const node = first ? el.querySelector<HTMLElement>(`[data-message-seq="${first.seq}"]`) : null;
      const target = el.querySelector(".unread-divider") ?? node;
      if (resume?.atBottom) {
        el.scrollTop = el.scrollHeight;
      } else if (resume && resumeNode) {
        el.scrollTop += resumeNode.getBoundingClientRect().top - el.getBoundingClientRect().top - resume.anchorOffset;
      } else if (first && target) {
        el.scrollTop += target.getBoundingClientRect().top - el.getBoundingClientRect().top - 12;
      } else {
        el.scrollTop = el.scrollHeight;
      }
      v.baseline = max;
      v.width = el.clientWidth;
      v.height = el.clientHeight;
      v.positioned = true;
      v.followNext = false;
      v.resume = null;
      v.prepend = null;
      render(n => n + 1);
    } else if (v.hold && v.hold.until > performance.now() && v.hold.node.isConnected) {
      el.scrollTop += v.hold.node.getBoundingClientRect().top - el.getBoundingClientRect().top - v.hold.offset;
    } else if (v.prepend) {
      const prepend = v.prepend;
      const anchor = prepend.anchorSeq ? el.querySelector<HTMLElement>(`[data-message-seq="${prepend.anchorSeq}"]`) : null;
      if (anchor) {
        el.scrollTop += anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - prepend.anchorOffset;
      } else {
        // Keep the previous height fallback for an empty/transient render.
        el.scrollTop = prepend.top + el.scrollHeight - prepend.height;
      }
      v.prepend = null;
    } else if (v.atBottom || v.followNext) {
      el.scrollTop = el.scrollHeight;
      v.followNext = false;
    }
    syncPosition();
  }, [isVisible, syncPosition]);

  useLayoutEffect(() => {
    position();
  });

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const effectVisit = visit.current;
    let touchY = 0;
    const pauseFollow = () => {
      const v = visit.current;
      if (!v?.positioned) return;
      v.hold = null;
      v.detached = true;
      v.resumeIntent = false;
      v.followNext = false;
      if (v.atBottom) {
        v.atBottom = false;
        render(n => n + 1);
      }
    };
    const resumeFollow = () => {
      const v = visit.current;
      if (v?.positioned && v.detached) v.resumeIntent = true;
    };
    const onWheel = (event: WheelEvent) => {
      if (event.deltaY < 0) pauseFollow();
      else if (event.deltaY > 0) resumeFollow();
    };
    const onTouchStart = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0; };
    const onDisclosure = (event: MouseEvent) => {
      const v = visit.current;
      const summary = (event.target as Element | null)?.closest?.("summary");
      if (!v?.positioned || !summary || !el.contains(summary)) return;
      v.hold = { node: summary as HTMLElement, offset: summary.getBoundingClientRect().top - el.getBoundingClientRect().top, until: performance.now() + HOLD_MS };
    };
    const onTouchMove = (event: TouchEvent) => {
      const y = event.touches[0]?.clientY ?? touchY;
      if (y > touchY) pauseFollow();
      else if (y < touchY) resumeFollow();
      touchY = y;
    };
    const onKey = (event: KeyboardEvent) => {
      if (["ArrowUp", "PageUp", "Home"].includes(event.key) || (event.key === " " && event.shiftKey)) pauseFollow();
      else if (["ArrowDown", "PageDown", "End"].includes(event.key) || (event.key === " " && !event.shiftKey)) resumeFollow();
    };
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchstart", onTouchStart, { passive: true });
    el.addEventListener("touchmove", onTouchMove, { passive: true });
    el.addEventListener("keydown", onKey);
    el.addEventListener("click", onDisclosure, true);
    const observer = new ResizeObserver(position);
    observer.observe(el);
    const content = el.querySelector(".message-list");
    if (content) observer.observe(content);
    const onFocus = () => { position(); markVisible(); };
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onFocus);
    const desktop = window.matchMedia("(min-width: 768px)");
    desktop.addEventListener("change", onFocus);
    return () => {
      if (visit.current === effectVisit) rememberVisit(effectVisit);
      observer.disconnect();
      el.removeEventListener("wheel", onWheel);
      el.removeEventListener("touchstart", onTouchStart);
      el.removeEventListener("touchmove", onTouchMove);
      el.removeEventListener("keydown", onKey);
      el.removeEventListener("click", onDisclosure, true);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onFocus);
      desktop.removeEventListener("change", onFocus);
    };
  }, [id, options.ready, position, markVisible]);

  const preservePrepend = useCallback(() => {
    const el = scrollRef.current;
    const v = visit.current;
    if (el && v?.positioned) {
      const snapshot = captureViewSnapshot(el, v);
      v.prepend = { top: el.scrollTop, height: el.scrollHeight, anchorSeq: snapshot.anchorSeq, anchorOffset: snapshot.anchorOffset };
    }
  }, []);

  const jumpLatest = useCallback(() => {
    const el = scrollRef.current;
    const v = visit.current;
    if (!el || !v) return;
    v.detached = false;
    v.resumeIntent = false;
    // An explicit jump supersedes a pending restore that may be loading
    // older pages for an anchor outside the current page.
    v.resume = null;
    v.hold = null;
    v.followNext = true;
    position();
  }, [position]);

  const retryBoundary = useCallback(() => {
    if (visit.current) { visit.current.boundaryFailed = false; render(n => n + 1); }
  }, []);

  const current = visit.current;
  const records = options.messages.filter(m => m.conversation_id === id);
  const firstUnread = records.find(m => m.seq > current.readSeq && m.seq <= (current.entryMax ?? 0) && m.kind !== "notice" && m.kind !== "message_ref");
  const boundaryLoaded = !options.hasMore || (records[0]?.seq ?? 0) <= current.readSeq + 1;
  const unreadSeq = boundaryLoaded ? firstUnread?.seq : undefined;
  const newCount = current.positioned && !current.atBottom
    ? records.filter(m => m.seq > current.baseline && m.role === "assistant" && m.kind !== "notice" && m.kind !== "message_ref").length : 0;
  const onScroll = useCallback(() => { syncPosition(true); }, [syncPosition]);

  return { scrollRef, onScroll, preservePrepend, jumpLatest, unreadSeq,
    showJump: current.positioned && !current.nearBottom, newCount, boundaryError: current.boundaryFailed, retryBoundary };
}
