import { Chapter, DemoCard, DemoGrid } from "../Chapter";
import { Station } from "../Station";
import { DesktopDemo } from "../demos/computer/DesktopDemo";
import { PairingDemo } from "../demos/computer/PairingDemo";
import { PermissionDemo } from "../demos/computer/PermissionDemo";

export function ComputerChapter() {
  return (
    <Chapter id="computer" title="电脑与终端" intro="Bot 在一台真的电脑上干活。你要随时看得见它在点哪里，也要随时能把鼠标拿回来。">
      <Station
        id="desktop"
        place="电脑 · 悬浮桌面"
        title="开机，看它干活，再把鼠标拿回来"
        lede={<>
          <p>开机时，屏幕先亮成一条线，再像老显像管一样展开，外圈亮起湖水青辉光。这也是设计系统里唯一允许的模糊光晕。</p>
          <p>Bot 的鼠标用弹簧追踪目标，不跳帧。按「接管控制」，鼠标从猫手里递给你，边框换成陶土色。</p>
        </>}
      >
        <DesktopDemo />
      </Station>
      <DemoGrid>
        <DemoCard title="设备配对" note="配对码像翻牌一样逐位落下，等待时连线在流动，手机确认后连线画成实线，中间打勾。"><PairingDemo /></DemoCard>
        <DemoCard title="Mac 权限引导" note="当前这一步的开关自己演示一遍怎么打开，直到你真的去打开它。每完成一步打一个勾。"><PermissionDemo /></DemoCard>
      </DemoGrid>
    </Chapter>
  );
}
