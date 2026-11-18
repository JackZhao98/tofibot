// The spec's own regex highlighter (frozen V3.0, src/bundle.src.js:486-494), one line at a time. Five token kinds and no
// dependency. Kept in its own module so only code that shows code pays for it; the entry never imports it.
export type SynKind = "com" | "str" | "num" | "kw" | "fn";
export type SynToken = { kind?: SynKind; text: string };

const KEYWORD = /^(import|from|as|def|return|const|let|var|function|if|elif|else|for|in|while|class|await|async|try|except|with|not|and|or|True|False|None|null|true|false|new)$/;
const TOKEN = /((?:^|(?<=\s))#.*$|(?:^|(?<=\s))\/\/.*$)|("(?:[^"\\]|\\.)*"|'(?:[^'\\]|\\.)*')|(\b\d+(?:\.\d+)?\b)|([A-Za-z_][A-Za-z0-9_]*)(?=\s*\()|([A-Za-z_][A-Za-z0-9_]*)|(\s+|[\s\S])/g;

/** Splits one line of code into tokens: comments, strings, numbers, keywords and calls get a kind; the rest is plain. */
export function highlightLine(source: string): SynToken[] {
  const out: SynToken[] = [];
  TOKEN.lastIndex = 0;
  let m: RegExpExecArray | null;
  while ((m = TOKEN.exec(source))) {
    if (m[1]) out.push({ kind: "com", text: m[1] });
    else if (m[2]) out.push({ kind: "str", text: m[2] });
    else if (m[3]) out.push({ kind: "num", text: m[3] });
    else if (m[4]) out.push({ kind: KEYWORD.test(m[4]) ? "kw" : "fn", text: m[4] });
    else if (m[5]) out.push(KEYWORD.test(m[5]) ? { kind: "kw", text: m[5] } : { text: m[5] });
    else out.push({ text: m[6] });
  }
  return out;
}
