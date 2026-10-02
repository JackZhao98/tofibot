import { WakeableCat } from "../../lib/WakeableCat";
import "./global.css";

/** Empty state: nothing here yet, so a cat naps on the dot grid; tap to wake it, tap again to say hi. */
export function EmptyDemo() {
  return (
    <div className="empty-demo">
      <WakeableCat config={{ shape: "loaf", pattern: "solid", palette: "ivory" }} name="糯米" size={84} />
      <p className="empty-title">还没有记忆</p>
      <p className="empty-note">Bot 记住的事会出现在这里。</p>
    </div>
  );
}
