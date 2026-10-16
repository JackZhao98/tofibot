/** Structural equality for polled JSON, so an unchanged response keeps the old object and React skips the re-render. */
export const sameData = (a: unknown, b: unknown) => a === b || JSON.stringify(a) === JSON.stringify(b);
