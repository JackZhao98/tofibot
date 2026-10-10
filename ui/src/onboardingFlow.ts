/** First-run onboarding: the pure rules for when it shows and where it resumes. The state itself lives on the server. */

export const ONBOARDING_STEPS = 3;
export type OnboardingStep = 1 | 2 | 3;

export type OnboardingState = {
  /** Last step reached: 0 never started. */
  step: number;
  completed: boolean;
  skipped: boolean;
};

export type OnboardingMode = "none" | "sheet" | "chip";

/** Reads the server's answer; anything that is not a state (older server, test stub) means "no onboarding". */
export function parseOnboardingState(value: unknown): OnboardingState | null {
  if (!value || typeof value !== "object") return null;
  const raw = value as Record<string, unknown>;
  if (typeof raw.step !== "number") return null;
  return { step: raw.step, completed: raw.completed === true, skipped: raw.skipped === true };
}

/**
 * sheet: the first-run sheet is open. chip: setup was skipped and "Finish setup" shows in the sidebar.
 * none: nothing to show, including every returning account that already has a model and a Bot.
 */
export function onboardingMode(
  state: OnboardingState | null,
  facts: { modelConfigured: boolean; botCount: number; forcedOpen?: boolean },
): OnboardingMode {
  if (!state || state.completed) return "none";
  const established = facts.modelConfigured && facts.botCount > 0;
  if (state.step === 0 && !state.skipped && established) return "none";
  if (facts.forcedOpen) return "sheet";
  if (state.skipped) return established ? "none" : "chip";
  return "sheet";
}

/** The step a reopened sheet starts on. The welcome is only for people who have not yet moved past it. */
export function resumeStep(state: OnboardingState, modelConfigured: boolean): OnboardingStep {
  if (modelConfigured) return state.step >= 3 ? 3 : 2;
  return state.step <= 1 && !state.skipped ? 1 : 2;
}

/** "2 of 3" on the sidebar chip: where Finish setup will land. */
export function chipStep(modelConfigured: boolean): OnboardingStep {
  return modelConfigured ? 3 : 2;
}
