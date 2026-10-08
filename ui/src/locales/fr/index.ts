// One lazy chunk per language: every namespace catalog in this directory.
export default import.meta.glob<Record<string, unknown>>("./*.json", { eager: true, import: "default" });
