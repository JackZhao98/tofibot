import { useEffect, useState, type ReactNode } from "react";

/** Mount only while pending: quick requests finish without displaying a loader. */
export function DelayedFeedback({ children, delay = 300 }: { children: ReactNode; delay?: number }) {
  const [visible, setVisible] = useState(false);
  useEffect(() => {
    const timer = window.setTimeout(() => setVisible(true), delay);
    return () => window.clearTimeout(timer);
  }, [delay]);
  return visible ? <>{children}</> : null;
}
