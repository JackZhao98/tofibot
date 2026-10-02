import { Chapter, DemoCard, DemoGrid } from "../Chapter";
import { Station } from "../Station";
import { ThemeToggle } from "../ThemeToggle";
import { IconRipple } from "../stations/IconRipple";
import { EmptyDemo } from "../demos/global/EmptyDemo";
import { LoadingDemo } from "../demos/global/LoadingDemo";
import "../demos/global/global.css";

export function GlobalChapter() {
  return (
    <Chapter id="global" title="全局" intro="到处都会用到的小东西：图标、空状态、等待。">
      <Station
        id="icons"
        place="全局 · 图标"
        title="144 个图标，各有各的动作"
        lede={<>
          <p>Tofi Rounded Icons 每个图标都带一段只播一次的语义动效：铃铛会晃，垃圾桶盖会掀，发送箭头往上冲。</p>
          <p>点任意一个图标，波浪从它开始按距离往外传；切换分类时，留下的图标滑到新位置。</p>
        </>}
      >
        <IconRipple />
      </Station>
      <DemoGrid>
        <DemoCard title="空状态" note="还没有内容的地方，放一只在点阵上睡觉的猫。点一下它会醒，再点一下会打招呼。"><EmptyDemo /></DemoCard>
        <DemoCard title="加载" note="等待不到 0.3 秒时什么都不闪；等得久了，才出现一只小猫，它会在你等的时候慢慢醒过来。两个按钮对比一下。"><LoadingDemo /></DemoCard>
        <DemoCard title="主题切换" note="新主题从你按下的那个按钮开始，画一个圆揭开整页。这里的开关和右上角的开关是同步的。">
          <div className="theme-demo"><ThemeToggle /><p>点这个开关</p></div>
        </DemoCard>
      </DemoGrid>
    </Chapter>
  );
}
