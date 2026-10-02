import { createElement, useEffect, useId, useRef, type SVGProps } from 'react';
import catalog from './catalog.json';
import { createTofiMotion, type IconMotion, type MotionCatalog } from './motion.mjs';
export { createTofiMotion } from './motion.mjs';

export type TofiIconName = keyof typeof catalog.icons;
export type TofiIconNode = readonly [string, Record<string, string | number>];
export type TofiIconDefinition = {
  label: string;
  category: string;
  tags: string;
  motion: IconMotion;
  nodes: readonly TofiIconNode[];
  filledNodes: readonly TofiIconNode[];
};

// JSON inference widens the generator's [element, attributes] tuples to arrays.
export const tofiIcons = catalog.icons as unknown as Record<TofiIconName, TofiIconDefinition>;
export const tofiIconCategories = catalog.categories;
export const tofiIconNames = Object.keys(tofiIcons) as TofiIconName[];

export type TofiIconProps = Omit<SVGProps<SVGSVGElement>, 'children' | 'dangerouslySetInnerHTML' | 'name' | 'title'> & {
  name: TofiIconName;
  size?: number | string;
  variant?: 'outline' | 'filled';
  /** Play the assigned motion on the containing button's hover, focus and click. */
  animated?: boolean;
  title?: string;
};

/** Decorative by default. Add title for a meaningful standalone icon. */
export function TofiIcon({ name, size = 24, variant = 'outline', animated = false, title, style, ...props }: TofiIconProps) {
  const titleId = useId();
  const svgRef = useRef<SVGSVGElement>(null);
  useEffect(() => {
    if (!animated || !svgRef.current) return;
    const motion = createTofiMotion(catalog as unknown as MotionCatalog);
    motion.attach(svgRef.current, name);
    return () => motion.destroy();
  }, [animated, name, variant]);
  const accessible = Boolean(title || props['aria-label'] || props['aria-labelledby']);
  return (
    <svg
      ref={svgRef}
      data-tofi-icon={name}
      data-tofi-variant={variant}
      style={{ overflow: 'visible', flexShrink: 0, ...style }}
      xmlns="http://www.w3.org/2000/svg"
      width={size}
      height={size}
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth={1.8}
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden={accessible ? undefined : true}
      role={accessible ? 'img' : undefined}
      aria-labelledby={title ? titleId : undefined}
      focusable="false"
      {...props}
    >
      {title && <title id={titleId}>{title}</title>}
      <g data-tofi-body="">
        {tofiIcons[name][variant === 'filled' ? 'filledNodes' : 'nodes'].map(([element, attributes], index) => (
          <g key={index} data-tofi-part={index}>
            {createElement(element, attributes)}
          </g>
        ))}
      </g>
    </svg>
  );
}
