import { useEffect, useState } from "react";
import { request } from "../api";
import { useTranslation } from "../i18n";

/** 999 -> "999", 1234 -> "1.2k", 12345 -> "12.3k", 63467 -> "63.5k", 1.2 million -> "1.2M". */
export function formatStars(count: number): string {
  if (!Number.isFinite(count) || count < 0) return "";
  if (count < 1000) return String(Math.floor(count));
  const trim = (value: number) => value.toFixed(1).replace(/\.0$/, "");
  const thousands = trim(count / 1000);
  return thousands === "1000" ? trim(count / 1e6) + "M" : count < 1e6 ? thousands + "k" : trim(count / 1e6) + "M";
}

/** Star counts for some repositories, read from TOFI's own server (which caches GitHub's public count for a day). A repository without a count is simply absent: no placeholder, no stale number. */
export function useRepoStars(repos: string[]): Record<string, number> {
  const [stars, setStars] = useState<Record<string, number>>({});
  const key = [...new Set(repos.filter(Boolean))].sort().join(",");
  useEffect(() => {
    if (!key) return;
    let live = true;
    void request<{ stars?: Record<string, number> }>(`/api/extensions/repo-stars?repos=${encodeURIComponent(key)}`)
      .then(value => { if (live) setStars(value.stars ?? {}); }).catch(() => { if (live) setStars({}); });
    return () => { live = false; };
  }, [key]);
  return stars;
}

/** A quiet GitHub source link: the official GitHub mark, the star count when the server has one. Opens the repository in a new tab. */
export function RepoLink({ repo, stars }: { repo: string; stars?: number }) {
  const { t } = useTranslation("extensions");
  const count = stars === undefined ? "" : formatStars(stars);
  return <a className="repo-link" href={`https://github.com/${repo}`} target="_blank" rel="noopener noreferrer" aria-label={count ? t("connections.repo.with_stars", { repo, count }) : t("connections.repo.label", { repo })}>
    <img className="repo-mark repo-mark-light" src="/brand/services/github-monochrome.svg" alt="" width="14" height="14" />
    <img className="repo-mark repo-mark-dark" src="/brand/services/github-inverse.svg" alt="" width="14" height="14" />
    <span>{repo}</span>{count && <span className="repo-stars" data-stars={stars}>★ {count}</span>}
  </a>;
}
