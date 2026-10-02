import { useRef } from "react";
import { DotField, type DotFieldHandle } from "./DotField";
import { ToyBench } from "./ToyBench";

export function Hero() {
  const field = useRef<DotFieldHandle>(null);
  const benchWrap = useRef<HTMLDivElement>(null);
  const heroRef = useRef<HTMLElement>(null);

  // Bench coordinates -> hero coordinates for the dot field.
  const onImpact = (x: number, y: number, strength: number) => {
    const bench = benchWrap.current?.getBoundingClientRect();
    const hero = heroRef.current?.getBoundingClientRect();
    if (!bench || !hero) return;
    field.current?.ripple(x + bench.left - hero.left, y + bench.top - hero.top, strength);
  };

  return (
    <section className="hero" ref={heroRef} aria-labelledby="lab-title">
      <DotField ref={field} />
      <div className="lab-wrap hero-inner">
        <div className="hero-copy">
          <h1 id="lab-title">Tofi 动效实验室</h1>
          <p className="hero-lede">
            这里每个动效都对应 Tofi 界面里的一个真实时刻：Bot 在干活，结果送到你面前，一封信寄出去。
            猫用的是产品里同一套程序化引擎，逐帧实时计算，没有视频，也没有 GIF。
          </p>
          <p className="hero-hint">拖起一只猫，甩出去。或者点一下。</p>
        </div>
        <div className="bench-wrap" ref={benchWrap}>
          <ToyBench onImpact={onImpact} />
        </div>
      </div>
    </section>
  );
}
