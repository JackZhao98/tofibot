import { Chapter, DemoCard, DemoGrid } from "../Chapter";
import { Station } from "../Station";
import { BotPanelDemo } from "../demos/bot/BotPanelDemo";
import { HoldDeleteDemo } from "../demos/bot/HoldDeleteDemo";

export function BotChapter() {
  return (
    <Chapter id="bot" title="Bot" intro="每个 Bot 都是一只有长相、有脾气的猫。点开它、给它换毛色，都应该像在摆弄一个真的小东西。">
      <Station
        id="bot-panel"
        place="Bot · 面板"
        title="侧栏里的猫，直接变成面板里的大猫"
        lede={<>
          <p>点开一个 Bot，侧栏里 46px 的那只猫放大、飞进面板，变成 132px。它们是同一只猫，不是两张图在切换。</p>
          <p>换毛色时，新颜色从你点的色块那边扩散过来，把猫染上；换身形时，猫跳出去，新身形弹进来。</p>
        </>}
      >
        <BotPanelDemo />
      </Station>
      <DemoGrid>
        <DemoCard title="按住删除" note="删除 Bot 要按住按钮，等红色填满才生效，中途松手会退回去。键盘按住空格也行。删掉之后那一行收起消失，还能撤销。" wide><HoldDeleteDemo /></DemoCard>
      </DemoGrid>
    </Chapter>
  );
}
