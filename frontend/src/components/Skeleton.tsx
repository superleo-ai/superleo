import type { CSSProperties } from "react";

// Grey shimmering placeholder shown during a page's first load.
export function Skeleton({ w, h = 12, r, style }: { w?: number | string; h?: number | string; r?: number | string; style?: CSSProperties }) {
  return <span className="skel" aria-hidden="true" style={{ width: w, height: h, borderRadius: r, ...style }} />;
}

const BAR_WIDTHS = ["92%", "78%", "64%", "86%", "54%"];

export function SkeletonLines({ n = 3, className }: { n?: number; className?: string }) {
  return (
    <div className={`skel-lines${className ? ` ${className}` : ""}`} aria-busy="true">
      {Array.from({ length: n }, (_, i) => <Skeleton key={i} h={11} w={BAR_WIDTHS[i % BAR_WIDTHS.length]} />)}
    </div>
  );
}

export function SkeletonRows({ n = 5, className }: { n?: number; className?: string }) {
  return (
    <div className={`skel-rows${className ? ` ${className}` : ""}`} aria-busy="true">
      {Array.from({ length: n }, (_, i) => (
        <div className="skel-row" key={i}>
          <Skeleton w={28} h={28} r="50%" />
          <div className="skel-row-text">
            <Skeleton h={11} w={i % 2 ? "32%" : "44%"} />
            <Skeleton h={10} w={BAR_WIDTHS[(i + 1) % BAR_WIDTHS.length]} />
          </div>
        </div>
      ))}
    </div>
  );
}

export function SkeletonCards({ n = 3, h = 96, className }: { n?: number; h?: number; className?: string }) {
  return (
    <div className={`skel-cards${className ? ` ${className}` : ""}`} aria-busy="true">
      {Array.from({ length: n }, (_, i) => (
        <div className="skel-card" key={i} style={{ minHeight: h }}>
          <Skeleton h={13} w={i % 2 ? "38%" : "52%"} />
          <Skeleton h={10} w="88%" />
          <Skeleton h={10} w="64%" />
        </div>
      ))}
    </div>
  );
}
