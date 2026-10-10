import { useRef } from "react";
import type { CatHandle } from "./CatStage";
import { CATS, EmptyCat, type CatLook, type CatPose } from "./EmptyCat";
import { useTranslation } from "./i18n";

const TROUPE: { look: CatLook; pose: CatPose; name: string }[] = [
  { look: CATS.miso, pose: "awake", name: "miso" },
  { look: CATS.nori, pose: "asleep", name: "nori" },
  { look: CATS.yuzu, pose: "awake", name: "yuzu" },
  { look: CATS.azuki, pose: "awake", name: "azuki" },
];

/** First run, before any Bot exists: the cats the product is about, and the one thing to do next. */
export function FirstRunHero({ onCreate, busy, error, side = false }: { onCreate: () => void; busy: boolean; error: string; /** Rendered inside the sidebar: shown only where the sidebar is the whole first screen (phones). */ side?: boolean }) {
  const { t } = useTranslation("chat");
  const cats = useRef<(CatHandle | null)[]>([]);
  const perk = () => cats.current.forEach((cat, index) => {
    window.setTimeout(async () => {
      const handle = cat?.get();
      if (!handle) return;
      if (handle.getState().idle === "asleep") { if (await cat!.play("wake")) cat!.setAutoplay(true); } else void cat!.play(index % 2 ? "look" : "curious");
    }, index * 110);
  });
  return (
    <section className={`first-run${side ? " first-run-side" : ""}`} data-first-run={side ? "side" : "pane"}>
      <div className="first-run-card">
        <div className="first-run-cats" aria-hidden="true">
          {TROUPE.map((cat, index) => (
            <EmptyCat key={cat.name} look={cat.look} pose={cat.pose} name={`first-run${side ? "-side" : ""}-${cat.name}`} className="first-run-cat" ref={handle => { cats.current[index] = handle; }} />
          ))}
        </div>
        <h1>{t("empty.first_bot_title")}</h1>
        <p>{t("empty.first_bot_body")}</p>
        <button type="button" className="primary-button first-run-create" disabled={busy} onClick={onCreate}
          onPointerEnter={perk} onFocus={perk}>
          {busy ? t("create.creating") : t("create.create_bot")}
        </button>
        {error && <p className="error-text" role="alert">{error}</p>}
      </div>
    </section>
  );
}
