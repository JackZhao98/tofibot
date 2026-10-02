import { Chapter } from "../Chapter";
import { Station } from "../Station";
import { StatusMorph } from "../stations/StatusMorph";
import { SidebarDemo } from "../demos/sidebar/SidebarDemo";

export function SidebarChapter() {
  return (
    <Chapter id="sidebar" title="侧栏" intro="一眼扫过去就要知道谁在干活、谁在等你、哪里出了问题。靠形状说话，不只靠颜色。">
      <Station
        id="status"
        place="侧栏 · 状态位"
        title="一个位置，五种状态"
        lede={<>
          <p>工作中是一颗呼吸的湖水青点，有新消息变成实心的蜂蜜金数字，要你决定是紫色问号，出错是红色菱形里的感叹号，完成只留一颗绿点。</p>
          <p>每种状态形状都不一样，色弱也能分清。形状之间的过渡用的是真实的弹簧曲线。</p>
        </>}
      >
        <StatusMorph />
      </Station>
      <Station
        id="sidebar-list"
        place="侧栏 · 会话列表"
        title="谁有新消息，谁就浮上来"
        lede={<>
          <p>来了新消息的会话浮到最上面，其他行平滑让位，不再瞬间跳动。新 Bot 从顶部挤进来，里面的猫从睡着到醒来。</p>
          <p>侧栏收成 80px 时，名字淡出，状态点收到头像角上。断线时左下角的点变红并抖一下，恢复时绿点弹回来。</p>
        </>}
      >
        <SidebarDemo />
      </Station>
    </Chapter>
  );
}
