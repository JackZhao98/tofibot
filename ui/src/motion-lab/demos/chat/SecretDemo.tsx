import { useState, type FormEvent } from "react";
import { TofiIcon } from "../../../icons";
import "./chat-cards.css";

type Phase = "asking" | "done";

/** The shackle is its own path so it can drop shut. */
function Lock({ closed }: { closed: boolean }) {
  return (
    <svg viewBox="0 0 24 24" className={`lock${closed ? " is-closed" : ""}`} aria-hidden="true">
      <path className="lock-shackle" d="M8 11V8a4 4 0 0 1 8 0v3" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
      <rect x="5" y="11" width="14" height="10" rx="3" fill="currentColor" />
      <circle cx="12" cy="16" r="1.6" fill="var(--surface)" />
    </svg>
  );
}

/** SecretInputCard: an ordinary password field; on submit the lock shuts and the card folds to one line. */
export function SecretDemo() {
  const [value, setValue] = useState("");
  const [phase, setPhase] = useState<Phase>("asking");

  // The value is dropped the moment it is submitted and never shown again.
  const submit = (event: FormEvent) => {
    event.preventDefault();
    if (!value || phase !== "asking") return;
    setValue("");
    setPhase("done");
  };

  return (
    <div className="secret-demo">
      <div className={`secret-card is-${phase}`}>
        <div className="secret-head">
          <span className="secret-lock"><Lock closed={phase === "done"} /></span>
          <span>{phase === "done" ? "已安全提交 · Ops Watcher 可以继续了" : "Ops Watcher 需要 GitHub token 才能继续"}</span>
        </div>
        <div className="secret-fold">
          <form className="secret-body" onSubmit={submit}>
            <input
              type="password"
              className="secret-input"
              value={value}
              placeholder="粘贴或输入 token"
              aria-label="GitHub token"
              autoComplete="off"
              spellCheck={false}
              data-1p-ignore
              data-lpignore="true"
              disabled={phase !== "asking"}
              onChange={(event) => setValue(event.target.value)}
            />
            <button type="submit" className="lab-btn primary" disabled={!value || phase !== "asking"}>提交</button>
          </form>
          <p className="secret-note">只交给这一次运行，不进聊天记录，也不会显示出来。</p>
        </div>
      </div>
      <div className="demo-actions">
        <button type="button" className="lab-btn" disabled={phase !== "done"} onClick={() => setPhase("asking")}><TofiIcon name="retry" size={16} animated />再来一次</button>
      </div>
    </div>
  );
}
