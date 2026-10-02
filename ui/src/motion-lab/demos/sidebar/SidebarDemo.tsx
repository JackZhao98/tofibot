import { useEffect, useRef, useState } from "react";
import { TofiIcon } from "../../../icons";
import type { AvatarConfig } from "../../../lib/tofi-avatar/index.js";
import { CatStage, type CatHandle } from "../../lib/CatStage";
import { BOTS } from "../../lib/crew";
import { flipList } from "../../lib/flip";
import { SPRINGS, springEasing } from "../../lib/spring";
import { prefersReducedMotion } from "../../lib/hooks";
import "./sidebar.css";

type Row = Readonly<{
  id: string;
  name: string;
  preview: string;
  time: string;
  unread: number;
  working: boolean;
  cat: Partial<AvatarConfig>;
  fresh?: boolean;
}>;

const REORDER = springEasing(SPRINGS.morph);
const INITIAL: readonly Row[] = [
  { id: "research", name: BOTS.research.name, preview: "正在读 API 文档", time: "14:32", unread: 0, working: true, cat: BOTS.research.cat },
  { id: "writer", name: BOTS.writer.name, preview: "周报拟好了，你看看", time: "14:20", unread: 1, working: false, cat: BOTS.writer.cat },
  { id: "ops", name: BOTS.ops.name, preview: "告警已恢复", time: "13:05", unread: 0, working: false, cat: BOTS.ops.cat },
  { id: "archivist", name: BOTS.archivist.name, preview: "归档了 12 份文件", time: "昨天", unread: 0, working: false, cat: BOTS.archivist.cat },
];
const NEWS = ["有个问题要你决定", "查到三篇相关文章", "服务又抖了一下", "整理完了，放在归档里"];
const NEWCOMER: Partial<AvatarConfig> = { shape: "stand", pattern: "tipped", palette: "pointlilac" };

/** A newcomer arrives asleep and wakes once it has slid into place. */
function RowCat({ cat, fresh }: { cat: Partial<AvatarConfig>; fresh: boolean }) {
  const handle = useRef<CatHandle>(null);
  useEffect(() => {
    if (!fresh) return;
    const timer = window.setTimeout(() => { void handle.current?.play("wake"); }, 520);
    return () => window.clearTimeout(timer);
    // Only the first mount of a newcomer wakes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  return <CatStage config={cat} initialState={fresh ? "asleep" : "awake"} ref={handle} />;
}

function Badge({ row, corner }: { row: Row; corner: boolean }) {
  if (row.unread) return <span key={`${row.unread}`} className={`side-badge${corner ? " is-corner" : ""}`} aria-label={`${row.unread} 条未读`}>{Math.min(row.unread, 99)}</span>;
  if (row.working) return <span className={`side-breath${corner ? " is-corner" : ""}`} aria-label="工作中" />;
  return null;
}

/** Sidebar: new messages float rows to the top, collapse to 80px, a newcomer slides in, connection drops and returns. */
export function SidebarDemo() {
  const listRef = useRef<HTMLDivElement>(null);
  const newsIndex = useRef(0);
  const newcomers = useRef(0);
  const [rows, setRows] = useState<readonly Row[]>(INITIAL);
  const [active, setActive] = useState("research");
  const [collapsed, setCollapsed] = useState(false);
  const [online, setOnline] = useState(true);
  const [rising, setRising] = useState<string | null>(null);

  const reorder = (update: (current: readonly Row[]) => readonly Row[]) => {
    flipList(listRef.current, () => setRows(update), { duration: REORDER.durationMs, easing: REORDER.easing, selector: "[data-flip]" });
  };

  // Someone below the top gets a message: it floats up, the rest make room.
  const incoming = () => {
    const candidates = rows.slice(1).filter((row) => row.id !== active);
    const target = candidates[newsIndex.current % Math.max(1, candidates.length)] ?? rows[1];
    if (!target) return;
    const text = NEWS[newsIndex.current++ % NEWS.length];
    setRising(target.id);
    reorder((current) => {
      const row = current.find((item) => item.id === target.id)!;
      return [{ ...row, preview: text, time: "刚刚", unread: row.unread + 1, working: false, fresh: false }, ...current.filter((item) => item.id !== target.id)];
    });
    window.setTimeout(() => setRising(null), REORDER.durationMs);
  };

  const join = () => {
    newcomers.current += 1;
    const id = `newcomer-${newcomers.current}`;
    reorder((current) => [{ id, name: newcomers.current > 1 ? `新 Bot ${newcomers.current}` : "新 Bot", preview: "你好，我刚加入", time: "刚刚", unread: 1, working: false, cat: NEWCOMER, fresh: true }, ...current].slice(0, 6));
  };

  const open = (id: string) => {
    setActive(id);
    setRows((current) => current.map((row) => (row.id === id ? { ...row, unread: 0 } : row)));
  };

  const toggleOnline = () => setOnline((value) => !value);
  const reduced = prefersReducedMotion();
  const activeRow = rows.find((row) => row.id === active);

  return (
    <div className="sidebar-demo">
      <div className={`stage side-stage${collapsed ? " is-collapsed" : ""}`}>
        <aside className="side-pane" aria-label="侧栏">
          <div className="side-top">
            <span className="side-logo" aria-hidden="true">tofi</span>
            <button type="button" className="side-toggle" aria-label={collapsed ? "展开侧栏" : "收起侧栏"} aria-pressed={collapsed} onClick={() => setCollapsed((value) => !value)}>
              <TofiIcon name="sidebar" size={18} />
            </button>
          </div>
          <div className="side-list" ref={listRef}>
            {rows.map((row) => (
              <div key={row.id} className={`side-slot${row.fresh && !reduced ? " is-fresh" : ""}`} data-flip={row.id}>
                <button
                  type="button"
                  className={`side-row${row.id === active ? " is-active" : ""}${rising === row.id ? " is-rising" : ""}`}
                  aria-label={row.name}
                  onClick={() => open(row.id)}
                >
                  <span className="side-avatar">
                    <RowCat cat={row.cat} fresh={Boolean(row.fresh)} />
                    <Badge row={row} corner />
                  </span>
                  <span className="side-copy">
                    <strong>{row.name}</strong>
                    <small key={row.preview}>{row.preview}</small>
                  </span>
                  <span className="side-meta">
                    <time>{row.time}</time>
                    <Badge row={row} corner={false} />
                  </span>
                </button>
              </div>
            ))}
          </div>
          <div className={`side-account${online ? "" : " is-offline"}`} role="status">
            <span className="side-me">你<i className="side-conn" key={String(online)} aria-hidden="true" /></span>
            <span className="side-copy">
              <strong>你</strong>
              <small>{online ? "tofi.local · 已连接" : "连接中断 · 正在重连"}</small>
            </span>
          </div>
        </aside>
        <div className="side-main" aria-hidden="true">
          <p className="side-main-title">{activeRow?.name ?? ""}</p>
          <span className="side-skeleton" style={{ width: "62%" }} />
          <span className="side-skeleton" style={{ width: "48%" }} />
          <span className="side-skeleton is-me" style={{ width: "36%" }} />
        </div>
      </div>
      <div className="stage-controls">
        <button type="button" className="lab-btn primary" onClick={incoming}><TofiIcon name="chat-unread" size={16} animated />来一条新消息</button>
        <button type="button" className="lab-btn" onClick={join}><TofiIcon name="bot-add" size={16} animated />新 Bot 加入</button>
        <button type="button" className="lab-btn" onClick={() => setCollapsed((value) => !value)}><TofiIcon name="sidebar" size={16} />{collapsed ? "展开侧栏" : "折叠侧栏"}</button>
        <button type="button" className="lab-btn quiet" onClick={toggleOnline}><TofiIcon name={online ? "offline" : "online"} size={16} />{online ? "断开连接" : "恢复连接"}</button>
      </div>
    </div>
  );
}
