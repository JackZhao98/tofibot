import {TofiIcon} from "./icons";
import {useTranslation} from "./i18n";
import {useComputerContents, type ComputerContents} from "./useComputerContents";
import "./computer-contents.css";

/**
 * What a wipe or a swap of the computer takes with it, with the names and counts the server holds
 * (docs/design/computer-decoupling.md section 7.5): the three kinds of computer state in words, the environment
 * variable and SSH key names, and the files that exist only on the computer. Mount it only while a computer is attached.
 */
export function ComputerLossList() {
  const {data, failed, retry} = useComputerContents();
  return <ComputerLossView data={data} failed={failed} retry={retry}/>;
}

/** The list without the request: `data` is null while it loads. */
export function ComputerLossView({data, failed, retry}: {data: ComputerContents | null; failed: boolean; retry: () => void}) {
  const {t} = useTranslation("computer");
  const env = data?.environment ?? [];
  const keys = data?.ssh_keys ?? [];
  const pending = data?.pending_file_copies ?? 0;
  return <div className="computer-loss" role="group" aria-label={t("loss.title")} aria-busy={!data && !failed}>
    <strong className="computer-loss-title">{t("loss.title")}</strong>
    <ul>
      <li>{t("loss.browser")}</li>
      <li>{t("loss.tools")}</li>
      <li>{t("loss.files")}</li>
      {data && env.length > 0 && <li>{t("loss.env", {count: env.length, names: env.join(", ")})}</li>}
      {data && keys.length > 0 && <li>{t("loss.keys", {count: keys.length, names: keys.join(", ")})}</li>}
      {data && env.length + keys.length === 0 && <li>{t("loss.no_config")}</li>}
      {data && pending > 0 && <li className="is-warn">{t("loss.pending", {count: pending})}</li>}
    </ul>
    {!data && !failed && <p className="computer-loss-note" role="status"><TofiIcon name="loading" size={14} aria-hidden="true"/>{t("loss.loading")}</p>}
    {failed && <p className="computer-loss-note is-error" role="alert">{t("loss.load_failed")} <button type="button" className="text-button" onClick={retry}>{t("contents.retry")}</button></p>}
  </div>;
}
