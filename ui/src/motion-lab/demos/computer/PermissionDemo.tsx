import { useState } from "react";
import { TofiIcon, type TofiIconName } from "../../../icons";
import "./computer.css";

const STEPS: readonly { id: string; label: string; why: string; icon: TofiIconName }[] = [
  { id: "accessibility", label: "辅助功能", why: "让 Bot 点按钮、填表单", icon: "cursor" },
  { id: "screen", label: "屏幕录制", why: "让 Bot 看得到屏幕", icon: "screen-share" },
  { id: "automation", label: "自动化", why: "让 Bot 控制访达和浏览器", icon: "workflow" },
];

/** PermissionCoach: the next switch demonstrates itself in a loop until you flip it; each step earns a check. */
export function PermissionDemo() {
  const [granted, setGranted] = useState<readonly boolean[]>(STEPS.map(() => false));
  const current = granted.indexOf(false);
  const allDone = current === -1;

  const grant = (index: number) => {
    if (index !== current) return;
    setGranted((all) => all.map((value, at) => (at === index ? true : value)));
  };

  return (
    <div className="perm-demo">
      <div className="perm-window" role="group" aria-label="系统设置 · 隐私与安全性">
        <div className="perm-titlebar"><i /><i /><i /><span>隐私与安全性</span></div>
        {STEPS.map((step, index) => (
          <div key={step.id} className={`perm-row${index === current ? " is-current" : ""}${granted[index] ? " is-granted" : ""}`}>
            <span className="perm-icon"><TofiIcon name={step.icon} size={18} /></span>
            <span className="perm-copy"><strong>{step.label}</strong><small>{step.why}</small></span>
            {granted[index] && <span className="perm-check" aria-hidden="true"><TofiIcon name="check" size={14} variant="filled" /></span>}
            <button
              type="button"
              role="switch"
              aria-checked={granted[index]}
              aria-label={`为 Tofi 打开${step.label}`}
              className="perm-switch"
              disabled={index !== current}
              onClick={() => grant(index)}
            >
              <span className="perm-knob" />
              {index === current && <span className="perm-ghost" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M4 3l15 7.5-6.5 1.8L9.8 19z" /></svg></span>}
            </button>
          </div>
        ))}
      </div>
      <p className="perm-status" role="status">{allDone ? "全部就绪，Bot 可以用这台 Mac 了。" : `第 ${current + 1} 步，共 ${STEPS.length} 步：打开「${STEPS[current].label}」`}</p>
      <div className="demo-actions">
        <button type="button" className="lab-btn quiet" disabled={!granted.some(Boolean)} onClick={() => setGranted(STEPS.map(() => false))}>重来</button>
      </div>
    </div>
  );
}
