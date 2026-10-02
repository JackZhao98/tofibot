export type MotionTrack = {
  frames: Keyframe[];
  duration: number;
  origin?: string;
  delay?: number;
};
export type IconMotion = {
  preset: string;
  accents?: (MotionTrack & { nodes: [string, Record<string, string>][] })[];
  parts?: (MotionTrack & { indices: number[]; variant?: string })[];
};
export type MotionCatalog = {
  motions: Record<string, MotionTrack & { label: string }>;
  icons: Record<string, { motion: IconMotion }>;
};
export type MotionController = {
  play(svg: SVGSVGElement | null, name?: string): void;
  attach(svg: SVGSVGElement, name: string, host?: Element): () => void;
  cancel(svg: SVGSVGElement): void;
  cancelAll(): void;
  setEnabled(value: boolean): void;
  destroy(): void;
};
export function createTofiMotion(catalog: MotionCatalog): MotionController;
