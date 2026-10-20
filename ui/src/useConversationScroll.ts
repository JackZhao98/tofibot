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
  glide: Glide | null;
  lastGrowth: number;
}

/** A follow-bottom scroll tween in flight. The target is read live every frame, so growth that lands mid-glide is followed. */
interface Glide { raf: number; from: number; start: number; duration: number; height: number; fresh: boolean; }

// A disclosure the reader opens grows the page under their pointer; follow-bottom
// must not chase that growth. Hold the clicked row in place while it settles.
const HOLD_MS = 900;
// The longest a loaded conversation may stay hidden while it waits for its position.
const SAFETY_MS = 200;
// Arrival glide: content above a newly arrived block rides up with a scroll tween instead of jumping. A whole message
// arriving uses --m-base; growth that follows a streaming draft is a shorter hop so the pane never trails the text.
// One frame never moves more than GLIDE_STEP_MAX px, and a hop longer than GLIDE_SPAN_MAX px jumps the excess first.
export const GLIDE_MS = 220;
export const GLIDE_STREAM_MS = 120;
const GLIDE_STEP_MAX = 24;
const GLIDE_SPAN_MAX = 300;
// A second growth within this window of the previous one is a stream, not a fresh arrival.
const GLIDE_STREAM_WINDOW_MS = 400;
const easeInOutSine = (p: number) => -(Math.cos(Math.PI * p) - 1) / 2;
const reducedMotion = () => typeof window.matchMedia === "function" && window.matchMedia("(prefers-reduced-motion: reduce)").matches;

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
    if (visit.current?.glide) { cancelAnimationFrame(visit.current.glide.raf); visit.current.glide = null; }
    const snapshot = id ? snapshots.current.get(id) ?? null : null;
    visit.current = { id, readSeq: options.conversation?.read_seq ?? 0, entryMax: snapshot?.entryMax ?? null,
      resume: snapshot, positioned: false, atBottom: snapshot?.atBottom ?? true, nearBottom: true, width: 0, height: 0,
      baseline: 0, detached: snapshot?.detached ?? false, resumeIntent: false, lastScrollTop: 0,
      prepend: null, followNext: false, loadingBoundary: false, boundaryFailed: false, hold: null, glide: null, lastGrowth: 0 };
  }

  const isVisible = useCallback(() => document.visibilityState === "visible" &&
    (!latest.current.mobileList || window.matchMedia("(min-width: 768px)").matches), []);

  const markVisible = useCallback(() => {
    const v = visit.current;
    const o = latest.current;
    if (!v?.positioned || !v.atBottom || !o.ready || !isVisible()) return;
    const seq = o.messages.reduce((max, m) => m.conversation_id === v.id && !m.local ? Math.max(max, m.seq) : max, 0);
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
    // A glide is following the bottom on purpose: the pane is mid-hop, not scrolled away.
    const bottom = distanceFromBottom < 48 || Boolean(v.glide && !v.detached);
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
      const seq = latest.current.messages.reduce((max, m) => m.conversation_id === v.id && !m.local ? Math.max(max, m.seq) : max, 0);
      if (v.baseline !== seq) { v.baseline = seq; render(n => n + 1); }
      markVisible();
    }
    if (changed) render(n => n + 1);
    rememberVisit(v);
  }, [markVisible]);

  const stopGlide = useCallback((v: Visit | null, land: boolean) => {
    const glide = v?.glide;
    if (!v || !glide) return;
    cancelAnimationFrame(glide.raf);
    v.glide = null;
    const el = scrollRef.current;
    if (land && el && visit.current === v) el.scrollTop = el.scrollHeight;
  }, []);

  /** Follow the bottom after the page grew. A tween instead of a jump; instant when motion is reduced or the page is hidden. */
  const followBottom = useCallback((el: HTMLDivElement, v: Visit) => {
    const target = () => el.scrollHeight - el.clientHeight;
    const gap = target() - el.scrollTop;
    if (gap <= 0.5 && !v.glide) return;
    const now = performance.now();
    if (reducedMotion() || document.visibilityState !== "visible") { stopGlide(v, false); el.scrollTop = el.scrollHeight; return; }
    const streaming = now - v.lastGrowth < GLIDE_STREAM_WINDOW_MS;
    const grew = !v.glide || v.glide.height !== el.scrollHeight;
    if (!grew) return;
    v.lastGrowth = now;
    if (gap > GLIDE_SPAN_MAX) el.scrollTop = target() - GLIDE_SPAN_MAX;
    const glide: Glide = v.glide ?? { raf: 0, from: 0, start: 0, duration: 0, height: 0, fresh: true };
    // The clock starts on the first painted frame: the render that brought the block can itself be a long task.
    glide.fresh = true;
    glide.duration = streaming ? GLIDE_STREAM_MS : GLIDE_MS;
    glide.height = el.scrollHeight;
    if (v.glide) return;
    v.glide = glide;
    const frame = (time: number) => {
      const node = scrollRef.current;
      if (v.glide !== glide || !node || visit.current !== v) return;
      const end = node.scrollHeight - node.clientHeight;
      if (glide.fresh) { glide.fresh = false; glide.from = node.scrollTop; glide.start = time; }
      const p = Math.min(1, Math.max(0, (time - glide.start) / glide.duration));
      const wanted = glide.from + (end - glide.from) * easeInOutSine(p);
      const next = Math.min(wanted, node.scrollTop + GLIDE_STEP_MAX);
      node.scrollTop = p >= 1 && end - node.scrollTop <= GLIDE_STEP_MAX ? end : Math.max(next, node.scrollTop);
      glide.height = node.scrollHeight;
      if (p >= 1 && Math.abs(end - node.scrollTop) <= 0.5) { v.glide = null; syncPosition(); return; }
      glide.raf = requestAnimationFrame(frame);
    };
    glide.raf = requestAnimationFrame(frame);
  }, [stopGlide, syncPosition]);

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
      stopGlide(v, false);
      const resume = v.resume;
      let resumeNode = resume?.anchorSeq ? el.querySelector<HTMLElement>(`[data-message-seq="${resume.anchorSeq}"]`) : null;
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
        // The anchor is older than everything loaded: keep the restore intent until paging brings it in.
        // This also covers the transient empty page while a switched conversation is loading and prevents
        // an accidental jump to bottom.
        if (records.length === 0 || (min > resume.anchorSeq && (o.hasMore || o.loadingOlder))) return;
        // The anchor's seq is inside the loaded range (or there is no more history) yet it has no node: it is
        // folded, hidden or superseded since the position was saved. Waiting would leave the pane hidden for
        // good, so settle on the nearest rendered message at or after it, and otherwise on the bottom.
        resumeNode = Array.from(el.querySelectorAll<HTMLElement>("[data-message-seq]")).find(node => Number(node.dataset.messageSeq) >= resume.anchorSeq) ?? null;
      }
      // The unread boundary is honoured only when it is inside the page already loaded. A backlog longer
      // than one page opens at the newest messages: paging older history in before the first paint made the
      // pane land at the top, then jump again for every page that arrived.
      const boundaryInPage = min <= v.readSeq + 1 || !o.hasMore;
      const first = resume || !boundaryInPage ? undefined : (v.followNext ? undefined : records.find(m => m.seq > v.readSeq));
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
      stopGlide(v, false);
      el.scrollTop += v.hold.node.getBoundingClientRect().top - el.getBoundingClientRect().top - v.hold.offset;
    } else if (v.prepend) {
      stopGlide(v, false);
      const prepend = v.prepend;
      const anchor = prepend.anchorSeq ? el.querySelector<HTMLElement>(`[data-message-seq="${prepend.anchorSeq}"]`) : null;
      if (anchor) {
        el.scrollTop += anchor.getBoundingClientRect().top - el.getBoundingClientRect().top - prepend.anchorOffset;
      } else {
        // Keep the previous height fallback for an empty/transient render.
        el.scrollTop = prepend.top + el.scrollHeight - prepend.height;
      }
      v.prepend = null;
    } else if (v.followNext) {
      // An explicit jump (send, "latest") lands at once; only organic growth glides.
      stopGlide(v, false);
      el.scrollTop = el.scrollHeight;
      v.followNext = false;
    } else if (v.atBottom) {
      followBottom(el, v);
    }
    syncPosition();
  }, [isVisible, syncPosition, stopGlide, followBottom]);

  useLayoutEffect(() => {
    position();
  });

  // Hard safety: loaded content waits for its first position (hidden, so it never paints in the wrong place),
  // but never longer than SAFETY_MS. If the restore cannot resolve by then, land on the newest messages.
  const safety = useRef<{ visit: Visit; timer: number } | null>(null);
  const armSafety = useCallback(() => {
    const v = visit.current;
    if (!v || v.positioned || !latest.current.ready || safety.current?.visit === v) return;
    if (safety.current) window.clearTimeout(safety.current.timer);
    const timer = window.setTimeout(() => {
      safety.current = null;
      if (visit.current !== v || v.positioned || !latest.current.ready || !isVisible()) return;
      v.resume = null;
      v.followNext = true;
      v.boundaryFailed = false;
      position();
    }, SAFETY_MS);
    safety.current = { visit: v, timer };
  }, [isVisible, position]);
  useLayoutEffect(() => {
    if (safety.current && (safety.current.visit.positioned || safety.current.visit !== visit.current)) { window.clearTimeout(safety.current.timer); safety.current = null; }
    armSafety();
  });
  useLayoutEffect(() => () => { if (safety.current) window.clearTimeout(safety.current.timer); safety.current = null; }, []);

  useLayoutEffect(() => {
    const el = scrollRef.current;
    if (!el) return;
    const effectVisit = visit.current;
    let touchY = 0;
    const pauseFollow = () => {
      const v = visit.current;
      if (!v?.positioned) return;
      stopGlide(v, false);
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
    // Any reading gesture takes over from the disclosure hold at once.
    const release = () => { if (visit.current) visit.current.hold = null; };
    // A gesture toward the bottom lets the hop land at once; one away from it (pauseFollow) abandons the hop where it is.
    const land = () => stopGlide(visit.current, true);
    const onWheel = (event: WheelEvent) => {
      release();
      if (event.deltaY < 0) pauseFollow();
      else if (event.deltaY > 0) { land(); resumeFollow(); }
    };
    const onTouchStart = (event: TouchEvent) => { touchY = event.touches[0]?.clientY ?? 0; };
    const onDisclosure = (event: MouseEvent) => {
      const v = visit.current;
      const summary = (event.target as Element | null)?.closest?.("summary");
      if (!v?.positioned || !summary || !el.contains(summary)) return;
      v.hold = { node: summary as HTMLElement, offset: summary.getBoundingClientRect().top - el.getBoundingClientRect().top, until: performance.now() + HOLD_MS };
    };
    const onTouchMove = (event: TouchEvent) => {
      release();
      const y = event.touches[0]?.clientY ?? touchY;
      if (y > touchY) pauseFollow();
      else if (y < touchY) { land(); resumeFollow(); }
      touchY = y;
    };
    const onKey = (event: KeyboardEvent) => {
      if (["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "].includes(event.key)) release();
      if (["ArrowUp", "PageUp", "Home"].includes(event.key) || (event.key === " " && event.shiftKey)) pauseFollow();
      else if (["ArrowDown", "PageDown", "End"].includes(event.key) || (event.key === " " && !event.shiftKey)) { land(); resumeFollow(); }
    };
    el.addEventListener("wheel", onWheel, { passive: true });
    el.addEventListener("touchstart", onTouchStart, { passive: true });
    el.addEventListener("touchmove", onTouchMove, { passive: true });
    el.addEventListener("keydown", onKey);
    el.addEventListener("click", onDisclosure, true);
    // Scrollbar drags send no wheel; a pointer press on the pane hands control back too.
    el.addEventListener("pointerdown", release, true);
    el.addEventListener("pointerdown", land, true);
    const observer = new ResizeObserver(position);
    observer.observe(el);
    const content = el.querySelector(".message-list");
    if (content) observer.observe(content);
    const onFocus = () => { position(); markVisible(); armSafety(); };
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
      el.removeEventListener("pointerdown", release, true);
      el.removeEventListener("pointerdown", land, true);
      stopGlide(effectVisit, false);
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onFocus);
      desktop.removeEventListener("change", onFocus);
    };
  }, [id, options.ready, position, markVisible, armSafety, stopGlide]);

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
    stopGlide(v, false);
    position();
  }, [position, stopGlide]);

  /** True when the reader has scrolled meaningfully away from the newest message. */
  const awayFromBottom = useCallback(() => {
    const el = scrollRef.current;
    return Boolean(el && el.scrollHeight - el.clientHeight - el.scrollTop > 48);
  }, []);

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

  // Content that is loaded but not yet positioned must never reach the screen: it would paint at the wrong
  // place and then jump. Only a pending restore that has to page in older history can keep it waiting.
  const unpositioned = options.ready && !current.positioned;

  return { scrollRef, onScroll, preservePrepend, jumpLatest, awayFromBottom, unpositioned, unreadSeq,
    showJump: current.positioned && !current.nearBottom, newCount, boundaryError: current.boundaryFailed, retryBoundary };
}
