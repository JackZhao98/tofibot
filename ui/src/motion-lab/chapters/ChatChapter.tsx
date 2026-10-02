import { Chapter, DemoCard, DemoGrid } from "../Chapter";
import { Station } from "../Station";
import { ToolSteps } from "../stations/ToolSteps";
import { SendFlight } from "../stations/SendFlight";
import { LetterSeal } from "../stations/LetterSeal";
import { QuestionDemo } from "../demos/chat/QuestionDemo";
import { MentionDemo } from "../demos/chat/MentionDemo";
import { CopyDemo } from "../demos/chat/CopyDemo";
import { DropDemo } from "../demos/chat/DropDemo";
import { ZoomDemo } from "../demos/chat/ZoomDemo";
import { SecretDemo } from "../demos/chat/SecretDemo";
import { ScheduleRowDemo } from "../demos/chat/ScheduleRowDemo";
import { MicDemo } from "../demos/chat/MicDemo";
import { ApprovalDemo } from "../demos/chat/ApprovalDemo";

export function ChatChapter() {
  return (
    <Chapter id="chat" title="对话" intro="你和 Bot 来回说话的地方。高频的动作要短、要直；等你处理的东西才配得上厚描边和一点仪式感。">
      <Station
        id="working"
        place="对话 · Bot 干活"
        title="干活时展开，干完收成一行"
        lede={<>
          <p>Bot 调用工具时，步骤列表夹在名字和回答之间，正在跑的那一步有呼吸灯和实时计时。</p>
          <p>回答写完，步骤列表整块折叠，飞进回答下方那行灰色小字；回答顺势上移，占住腾出来的位置。点那行小字可以再展开。</p>
        </>}
      >
        <ToolSteps />
      </Station>
      <Station
        id="send"
        place="对话 · 输入框"
        title="发出去的消息，从输入框冲进对话"
        lede={<>
          <p>默认表演版：消息气泡从输入框起飞，沿弧线冲到对话右侧，已有消息同步上移。克制版留作对照。</p>
          <p>写作搭子趴在输入框上沿：你发出去它会抬头，想的时候在键盘上敲；自然有两个意思时，回复会分成两条并隔一拍送达。</p>
        </>}
      >
        <SendFlight />
      </Station>
      <Station
        id="question"
        place="对话 · 问题卡"
        title="等你决定时站出来，答完就退下"
        lede={<>
          <p>Bot 需要你拍板时，问题卡从输入框上方升起，是整个对话里最厚重的一张卡。</p>
          <p>选一个选项：它先按下去，其余选项收起，整张卡收成一行「已回答」。没在时限内回答的，会从厚描边降成细线，一眼能看出不用管了。</p>
        </>}
      >
        <QuestionDemo />
      </Station>
      <Station
        id="approval"
        place="对话 · ApprovalCard 审批卡"
        title="把影响讲清楚，再等你点头"
        lede={<><p>蜂蜜色标识提醒你处理，动作、对象和影响放在同一块信息区。批准和拒绝都有明确的按钮。</p><p>下方可切换私信与群聊，查看生产环境的邮件草稿卡：私信没有头像和姓名，卡片填满消息列；群聊保留身份。演示数据不会联系 Gmail。</p></>}
      ><ApprovalDemo /></Station>
      <Station
        id="mention"
        place="对话 · @ 菜单"
        title="@ 一下，名字飞进输入框"
        lede={<>
          <p>菜单从你打 @ 的位置长出来。每只猫后面是它的实时状态，已经输入的部分下面有一条紫色下划线跟着移动。</p>
          <p>选中后，名字变成一个 chip，从菜单飞进输入框。</p>
        </>}
      >
        <MentionDemo />
      </Station>
      <Station
        id="letter"
        place="对话 · 拟稿确认"
        title="折好，装进信封，盖戳寄走"
        lede={<>
          <p>Bot 拟好的邮件是一张信纸：航空信条纹、横线纸、盖着猫的邮票。等你确认前，它是整页里最厚重的那张卡。</p>
          <p>按下「发送邮件」，信纸真的按三折叠起来，塞进信封，合上封口，贴邮票、盖邮戳，然后飞走。卡片最后收成一行平平的「已发送」。</p>
        </>}
      >
        <LetterSeal />
      </Station>
      <Station id="scheduled-run" place="对话 · ScheduledRun 定时任务" title="触发的任务，留一张时间票根"
        lede={<><p>左边保留计划触发时间，右边显示任务、频率和这一次的执行状态。点开卡片查看任务原文，再点一次平滑收起。</p><p>执行中的湖水青点会呼吸，秒数持续走；确认结果写入对话后显示绿点，失败时保留原因和重试入口。</p></>}
      ><ScheduleRowDemo /></Station>
      <DemoGrid>
        <DemoCard title="复制成功" note="复制图标原地转成绿色对勾，停一下再转回来。按钮会真的复制这段文字。"><CopyDemo /></DemoCard>
        <DemoCard title="拖文件进来" note="拖一个文件到卡片上：点阵投放框亮起，松手后文件卡片落进输入框，输入框的粗描边画出上传进度。只读文件名和大小，不会上传。"><DropDemo /></DemoCard>
        <DemoCard title="图片放大" note="缩略图本身放大到全屏，关闭时缩回原来的格子。Esc 或点背景都能关。"><ZoomDemo /></DemoCard>
        <DemoCard title="私密输入" note="输入框就是普通的密码框，选中、粘贴都照常。提交后锁扣合上，卡片收成一行，内容不再显示。"><SecretDemo /></DemoCard>
        <DemoCard title="录音波形" note="点「用我的麦克风」，波形会跟着你说话的音量跳。"><MicDemo /></DemoCard>
      </DemoGrid>
    </Chapter>
  );
}
