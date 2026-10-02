import { Chapter } from "../Chapter";
import { Station } from "../Station";
import { HandoffRelay } from "../stations/HandoffRelay";

export function TeamChapter() {
  return (
    <Chapter id="team" title="团队" intro="几只猫一起干一件事时，要看得见活是怎么从一只手传到另一只手的。">
      <Station
        id="relay"
        place="团队 · 协作链"
        title="一张 ticket 绕团队一圈"
        lede={<>
          <p>你发起一件事，研究助理拆分派发，写作搭子执行，Ops Watcher 核查，最后回到你手里。</p>
          <p>ticket 沿着同一条路径走，走过的地方由虚线描成实线；到哪只猫，哪只猫就接手干活。</p>
        </>}
      >
        <HandoffRelay />
      </Station>
    </Chapter>
  );
}
