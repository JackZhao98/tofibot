import { Chapter, DemoCard, DemoGrid } from "../Chapter";
import { McpDemo } from "../demos/settings/McpDemo";
import { OAuthDemo } from "../demos/settings/OAuthDemo";
import { SaveBarDemo } from "../demos/settings/SaveBarDemo";
import { UsageDemo } from "../demos/settings/UsageDemo";

export function SettingsChapter() {
  return (
    <Chapter id="settings" title="设置与连接" intro="连上一个服务、保存一次设置，都应该有一个清楚的「成了」或者「没成，因为什么」。">
      <DemoGrid>
        <DemoCard title="MCP 连接测试" note="测试时插头慢慢靠近插座。成功就「咔哒」插上、冒几颗火星；失败就弹回来，并给出一句原因。勾选下面的框可以看失败的样子。"><McpDemo /></DemoCard>
        <DemoCard title="账户授权" note="等你在浏览器里授权时，猫和服务之间的虚线一直在流动。授权完成，虚线画成实线，猫醒过来。"><OAuthDemo /></DemoCard>
        <DemoCard title="设置保存栏" note="改了任何一项，保存栏从底部弹上来。保存时显示进度，成功后打勾再收回去；放弃会把改动退回。"><SaveBarDemo /></DemoCard>
        <DemoCard title="用量面板" note="数字像里程表一样滚上来，柱状图从底部长出来，只在第一次看到时播放。"><UsageDemo /></DemoCard>
      </DemoGrid>
    </Chapter>
  );
}
