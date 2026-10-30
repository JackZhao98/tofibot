/** After an attach the computer's control socket needs a moment: reads of its resources may fail for up to this long. */
export const SETTLE_MS = 20_000;
/** Wait before retry number `attempt` (0-based): 1 s, 2 s, then 3 s. */
export const settleDelay = (attempt: number) => Math.min(3000, 1000 * (attempt + 1));

let attachedUntilMs = 0;
/** An attach just succeeded: the page that appears for the new computer (it mounts after this) settles quietly too. */
export const markAttached = () => { attachedUntilMs = Date.now() + SETTLE_MS; };
export const attachedUntil = () => attachedUntilMs;
