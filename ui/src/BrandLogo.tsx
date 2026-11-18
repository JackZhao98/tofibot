import "./ds/Brand/Brand.css";

/** Product artwork; originals remain in design/logo. The calico cat is the logo. Black and white are single-ink marks for places
 *  that print one colour; the old colour variant is gone (V3 PLAN S13). The look is the kit's `tb-brand`; `brand-logo` stays as the
 *  hook that layout sheets (sidebar rail, owner card) still size. */
export function BrandLogo({ variant = "calico" }: { variant?: "black" | "white" | "calico" }) {
  // A single-ink mark carries its own ink: dark on light grounds, paper on dark ones, whatever the theme.
  const ink = variant === "black" ? "var(--on-block)" : variant === "white" ? "var(--letter-paper)" : undefined;
  return <span className={`tb-brand brand-logo brand-logo--${variant}`} style={ink ? { color: ink } : undefined} aria-label="Tofi">
    <img src={variant === "calico" ? "/brand/tofi-logo-calico-awake.svg?v=awake-calico-3" : `/brand/tofi-logo-${variant}.svg`} alt="" width="44" height="44" />
    <span className="tb-brand-word" aria-hidden="true">tofi</span>
  </span>;
}
