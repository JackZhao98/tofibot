import { Hero } from "./hero/Hero";
import { ThemeToggle } from "./ThemeToggle";
import { ChapterNav } from "./ChapterNav";
import { ChatChapter } from "./chapters/ChatChapter";
import { SidebarChapter } from "./chapters/SidebarChapter";
import { BotChapter } from "./chapters/BotChapter";
import { TeamChapter } from "./chapters/TeamChapter";
import { ComputerChapter } from "./chapters/ComputerChapter";
import { SettingsChapter } from "./chapters/SettingsChapter";
import { GlobalChapter } from "./chapters/GlobalChapter";
import { useReducedMotion } from "./lib/hooks";

export function MotionLab() {
  const reduced = useReducedMotion();
  return (
    <div className="lab">
      <nav className="lab-bar" aria-label="动效实验室">
        <div className="lab-wrap lab-bar-inner">
          <a className="lab-brand" href="/">
            <img src="/brand/tofi-logo-color.svg" alt="" width={28} height={28} />
            <span>Tofi</span>
          </a>
          <span className="lab-bar-title">动效实验室</span>
          <ChapterNav />
          <span className="lab-bar-spacer" />
          {reduced && <span className="lab-reduced">已开启减少动态效果：动画会直接跳到结果</span>}
          <ThemeToggle />
        </div>
      </nav>
      <main>
        <Hero />
        <div className="lab-wrap stations">
          <ChatChapter />
          <SidebarChapter />
          <BotChapter />
          <TeamChapter />
          <ComputerChapter />
          <SettingsChapter />
          <GlobalChapter />
        </div>
      </main>
      <footer className="lab-footer">
        <div className="lab-wrap">
          猫、图标和颜色都直接取自 Tofi 生产代码：<code>lib/tofi-avatar</code>、<code>icons</code>、<code>design-tokens.css</code>。系统开启「减少动态效果」时，所有动画直接跳到结果。
        </div>
      </footer>
    </div>
  );
}
