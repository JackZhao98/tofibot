import { createContext, useContext, type ReactNode } from "react";
import type { Root, Element } from "hast";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import remarkCjkFriendly from "remark-cjk-friendly/parseOnly";
import { useTranslation } from "./i18n";

// Stable renderer identities preserve focus and horizontal scroll during chat updates.
// Renderers are components, so each reads the active language itself.
const components: Components = {
  a: ({ children, href }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
  table: ({ children }) => {
    const { t } = useTranslation("chat");
    return <div className="message-table" tabIndex={0} role="region" aria-label={t("markdown.table")}><table>{children}</table></div>;
  },
  pre: ({ children }) => {
    const { t } = useTranslation("chat");
    return <pre className="message-code" tabIndex={0} role="region" aria-label={t("markdown.code")}>{children}</pre>;
  },
  img: ({ src, alt }) => {
    const { t } = useTranslation("chat");
    return src?.startsWith("/api/attachments/")
      ? <img className="message-image" src={src} alt={alt ?? t("markdown.image")} loading="lazy" />
      : <a href={src} target="_blank" rel="noopener noreferrer">{alt || t("markdown.view_image")}</a>;
  },
};

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

function appendCursor() {
  return (tree: Root) => {
    const cursor: Element = { type: "element", tagName: "span", properties: { "data-typing-cursor": true }, children: [] };
    const last = tree.children.at(-1);
    if (last?.type === "element" && last.tagName === "p") last.children.push(cursor);
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

export function MessageMarkdown({ content, mention, cursor = false }: { content: string; mention?: ReactNode; cursor?: boolean }) {
  return <MentionContext.Provider value={mention}><Markdown remarkPlugins={[remarkGfm, remarkCjkFriendly]} rehypePlugins={[...(mention ? [prependMention] : []), ...(cursor ? [appendCursor] : [])]} skipHtml components={mentionComponents}>{content}</Markdown></MentionContext.Provider>;
}
