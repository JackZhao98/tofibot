import type { Action, CatAvatar } from "./lib/tofi-avatar/index.js";

export type AvatarMotion = "sleeping" | "awake" | "working";

// Keyboard work is the baseline. A brief paw gesture is occasional, and the
// next clip always returns to work rather than chaining unrelated tricks.
export function chooseWorkingAction(random: () => number = Math.random): Action {
  const roll = random();
  return roll < 0.78 ? "work" : roll < 0.93 ? "knead" : "bat";
}

export async function directAvatarMotion(
  cat: CatAvatar,
  previous: AvatarMotion,
  desired: AvatarMotion,
  isCurrent: () => boolean,
  pause: (ms: number) => Promise<void>,
  random: () => number = Math.random,
  allowAutoplay = true,
): Promise<void> {
  if (!isCurrent()) return;
  cat.setAutoplay(false);
  const play = async (action: Action): Promise<boolean> => {
    if (!isCurrent()) return false;
    try {
      const result = await cat.play(action);
      return isCurrent() && !result.cancelled;
    } catch {
      // A changed shape or an unmounted avatar may revoke this sequence.
      return false;
    }
  };

  if (previous === "working" && desired !== "working" && cat.getState().idle === "awake") {
    if (!await play("work-end")) return;
  }

  if (desired === "sleeping") {
    if (cat.getState().idle === "awake" || cat.getState().action === "wake") {
      if (!await play("sleep")) return;
    }
    if (isCurrent() && allowAutoplay) cat.setAutoplay(true);
    return;
  }

  if (cat.getState().idle === "asleep" || cat.getState().action === "sleep") {
    if (!await play("wake")) return;
  }
  if (desired === "awake") {
    if (isCurrent() && allowAutoplay) cat.setAutoplay(true);
    return;
  }

  let first = true;
  let returnToWork = false;
  while (isCurrent()) {
    // Reduced motion must never spin through instantly completed clips.
    if (cat.getState().reducedMotion) {
      await pause(1000);
      continue;
    }
    const action: Action = first || returnToWork ? "work" : chooseWorkingAction(random);
    first = false;
    returnToWork = action !== "work";
    if (!await play(action)) return;
    if (isCurrent()) await pause(180 + Math.round(random() * 320));
  }
}
