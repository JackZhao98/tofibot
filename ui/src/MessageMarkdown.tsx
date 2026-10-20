import { Children, createContext, isValidElement, memo, useContext, useState, type ReactNode } from "react";
import type { Root, Element } from "hast";
import Markdown, { defaultUrlTransform, type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkCjkFriendly from "remark-cjk-friendly/parseOnly";
import { useTranslation } from "./i18n";
import { attachmentIdFromHref, attachmentUrl, openAttachmentById, relativeLinkName, resolveAttachmentByName } from "./filePreviewModel";

// Inside the file viewer, document links push onto the viewer stack (Back returns).
export const ViewerLinkContext = createContext(false);
const urlTransform = (url: string) => /^attachment:/i.test(url) ? url : defaultUrlTransform(url);

function MarkdownLink({ href, children }: { href?: string; children?: ReactNode }) {
  const inViewer = useContext(ViewerLinkContext);
  const id = attachmentIdFromHref(href);
  const byName = !id && inViewer ? relativeLinkName(href) : null;
  const sibling = byName ? resolveAttachmentByName(byName) : undefined;
  if (id || sibling) {
    const target = id ? id : sibling!.id;
    return <a href={attachmentUrl(target)} className="attachment-link" data-attachment-id={target} onClick={event => {
      if (event.button !== 0 || event.metaKey || event.ctrlKey || event.shiftKey) return;
      event.preventDefault();
      openAttachmentById(target, { push: inViewer });
    }}>{children}</a>;
  }
  if (byName) return <span className="unresolved-link" title={href}>{children}</span>;
  return <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>;
}


// Stable renderer identities preserve focus and horizontal scroll during chat updates.
// Renderers are components, so each reads the active language itself.
const components: Components = {
  a: MarkdownLink,
  table: ({ children }) => {
    const { t } = useTranslation("chat");
    return <div className="message-table" tabIndex={0} role="region" aria-label={t("markdown.table")}><table>{children}</table></div>;
  },
  pre: CodeBlock,
  img: ({ src, alt }) => {
    const { t } = useTranslation("chat");
    return src?.startsWith("/api/attachments/")
      ? <img className="message-image" src={src} alt={alt ?? t("markdown.image")} loading="lazy" />
      : <a href={src} target="_blank" rel="noopener noreferrer">{alt || t("markdown.view_image")}</a>;
  },
};

const CODE_COLLAPSE_LINES = 16;
function nodeText(node: ReactNode): string {
  if (typeof node === "string" || typeof node === "number") return String(node);
  if (Array.isArray(node)) return node.map(nodeText).join("");
  return isValidElement<{ children?: ReactNode }>(node) ? nodeText(node.props.children) : "";
}

// Sunk block: language and line count in a header, copy always visible, long code folded.
function CodeBlock({ children }: { children?: ReactNode }) {
  const { t } = useTranslation("chat");
  const [copied, setCopied] = useState(false);
  const [expanded, setExpanded] = useState(false);
  const code = Children.toArray(children).find(isValidElement<{ className?: string }>);
  const language = /language-([\w+#.-]+)/.exec(code?.props.className ?? "")?.[1] ?? "";
  const text = nodeText(children).replace(/\n$/, "");
  const lines = text.split("\n").length;
  const long = lines > CODE_COLLAPSE_LINES;
  async function copy() {
    try { await navigator.clipboard.writeText(text); setCopied(true); window.setTimeout(() => setCopied(false), 1800); } catch { /* clipboard may be blocked; the text stays selectable */ }
  }
  return <div className="message-code-block">
    <div className="message-code-head"><span className="message-code-meta">{[language, t("markdown.lines", { count: lines })].filter(Boolean).join(" · ")}</span><button type="button" className="message-code-copy" onClick={() => void copy()}>{copied ? t("markdown.copied") : t("markdown.copy")}</button></div>
    <pre className={`message-code${long && !expanded ? " is-collapsed" : ""}`} tabIndex={0} role="region" aria-label={t("markdown.code")}>{children}</pre>
    {long && <button type="button" className="message-code-more" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}>{expanded ? t("markdown.show_less") : t("markdown.show_more", { count: lines - CODE_COLLAPSE_LINES })}</button>}
  </div>;
}

// Insert a structured mention after Markdown parsing; task text cannot create one.
function prependMention() {
  return (tree: Root) => {
    const mention: Element = { type: "element", tagName: "span", properties: { "data-message-mention": true }, children: [] };
    const first = tree.children[0];
    if (first?.type === "element" && first.tagName === "p") {
      first.children.unshift(mention, { type: "text", value: " " });
    } else {
      tree.children.unshift({ type: "element", tagName: "p", properties: {}, children: [mention] });
    }
  };
}

/** The inline container at the very end of the text: a cursor placed there adds no block, so the line never jumps when it is removed. */
function tailInline(node: Root | Element): Root | Element | undefined {
  const last = [...node.children].reverse().find((child): child is Element => child.type === "element");
  if (!last) return undefined;
  if (last.tagName === "p" || /^h[1-6]$/.test(last.tagName)) return last;
  if (last.tagName === "ul" || last.tagName === "ol") {
    const item = [...last.children].reverse().find((child): child is Element => child.type === "element" && child.tagName === "li");
    return item ? tailInline(item) ?? item : undefined;
  }
  if (last.tagName === "blockquote") return tailInline(last);
  if (last.tagName === "pre") return [...last.children].find((child): child is Element => child.type === "element" && child.tagName === "code") ?? last;
  return undefined;
}

function appendCursor() {
  return (tree: Root) => {
    const cursor: Element = { type: "element", tagName: "span", properties: { "data-typing-cursor": true }, children: [] };
    const target = tailInline(tree);
    if (target) target.children.push(cursor);
    else tree.children.push({ type: "element", tagName: "p", properties: {}, children: [cursor] });
  };
}

const MentionContext = createContext<ReactNode>(undefined);
const mentionComponents: Components = {
  ...components,
  span: ({ node, children }) => {
    const mention = useContext(MentionContext);
    return node?.properties["data-message-mention"] ? <>{mention}</> : node?.properties["data-typing-cursor"] ? <span className="typing-cursor" aria-hidden="true" /> : <span>{children}</span>;
  },
};

/** Memoized: parsing a long conversation's Markdown on every unrelated Workspace commit costs tens of milliseconds. */
export const MessageMarkdown = memo(function MessageMarkdown({ content, mention, cursor = false }: { content: string; mention?: ReactNode; cursor?: boolean }) {
  return <MentionContext.Provider value={mention}><Markdown remarkPlugins={[remarkGfm, remarkCjkFriendly]} rehypePlugins={[...(mention ? [prependMention] : []), ...(cursor ? [appendCursor] : [])]} skipHtml urlTransform={urlTransform} components={mentionComponents}>{content}</Markdown></MentionContext.Provider>;
});
