/** The Bot's one line of role is the first line of its instructions. The Bot card and Bot settings both read and write it through these two. */
export const roleOf = (instructions: string): string => instructions.trimStart().split("\n")[0]?.trim() ?? "";

/** Sets the first line and keeps the rest exactly. An empty role removes the line (no leading newline), so the next line becomes the role in both views. */
export function withRole(instructions: string, role: string): string {
  const body = instructions.trimStart();
  const at = body.indexOf("\n");
  const rest = at < 0 ? "" : body.slice(at + 1);
  const line = role.trim();
  return line ? (at < 0 ? line : `${line}\n${rest}`) : rest;
}
