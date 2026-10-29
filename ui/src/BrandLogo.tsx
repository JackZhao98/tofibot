import "./BrandLogo.css";

/** Product artwork; originals remain in design/logo. */
export function BrandLogo({ variant = "color" }: { variant?: "color" | "black" | "white" | "calico" }) {
  return <span className={`brand-logo brand-logo--${variant}`} aria-label="Tofi">
    <img src={variant === "calico" ? "/brand/tofi-logo-calico-awake.svg?v=awake-calico-2" : `/brand/tofi-logo-${variant}.svg`} alt="" width="44" height="44" />
    <span aria-hidden="true">tofi</span>
  </span>;
}
