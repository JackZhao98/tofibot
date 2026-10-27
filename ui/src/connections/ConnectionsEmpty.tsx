import { CATS, EmptyState } from "../EmptyCat";
import { ServiceMark } from "../ServiceMark";
import { useTranslation } from "../i18n";

/** The three services an empty page offers first (Claude.ai opens on its directory too). */
export const EMPTY_TILES = [{ id: "google", name: "Google" }, { id: "notion", name: "Notion" }, { id: "github", name: "GitHub" }] as const;
export type EmptyTileId = (typeof EMPTY_TILES)[number]["id"];

/** Settings › Connections with nothing connected: the cat, one line, three tiles that start a connection, Browse all. */
export function ConnectionsEmpty({ onStart, onBrowse }: { onStart: (id: EmptyTileId) => void; onBrowse: () => void }) {
  const { t } = useTranslation("extensions");
  return <div className="mcp-empty">
    <EmptyState name="mcp-empty" className="extension-empty" look={CATS.yuzu} pose="curious" size={72} title={t("mcp.empty")}><p>{t("mcp.empty_body")}</p></EmptyState>
    <div className="mcp-empty-tiles">{EMPTY_TILES.map(tile => <button key={tile.id} type="button" className="mcp-empty-tile" data-integration-id={tile.id} onClick={() => onStart(tile.id)}><ServiceMark name={tile.name} /><span>{tile.name}</span></button>)}</div>
    <button type="button" className="text-button mcp-empty-browse" onClick={onBrowse}>{t("mcp.browse_all")}</button>
  </div>;
}
