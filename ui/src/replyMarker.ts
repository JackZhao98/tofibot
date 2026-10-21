/**
 * The reply-to marker a message may start with: `[message_id=<uuid>]`, one or
 * more, optionally preceded by whitespace and followed by a space, a newline or
 * the text itself. It is syntax for the UI, never shown as text.
 */
const MARKER = /^\s*\[message_id=([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})\]\s*/i;
/** A marker still arriving: streaming can pause anywhere inside it. */
const PARTIAL = /^\s*\[message_id(?:=[0-9a-f-]*)?$/i;

export function readReplyMarker(content: string): { ids: string[]; body: string } {
  const ids: string[] = [];
  let body = content;
  for (let marker = MARKER.exec(body); marker; marker = MARKER.exec(body)) {
    ids.push(marker[1].toLowerCase());
    body = body.slice(marker[0].length);
  }
  if (ids.length) return { ids, body: PARTIAL.test(body) ? "" : body };
  // Never flash the internal marker as text while it streams in.
  if (PARTIAL.test(content)) return { ids, body: "" };
  return { ids, body: content };
}
