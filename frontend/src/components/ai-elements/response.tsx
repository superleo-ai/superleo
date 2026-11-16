import { cn } from "@/lib/utils";
import { memo } from "react";
import type { MouseEvent } from "react";
import { OpenWorkspaceFileExternal } from "../../../wailsjs/go/app/App";
import { isExternallyOpenable, openExternal } from "@/lib/openExternal";
import { toast } from "@/lib/toasts";
import type { ComponentProps } from "react";
import { AIRenderer } from "@ai-gui/react";
import {
  APP_LOCALE,
  normalizeOneLineBlocks,
  nodeRenderers,
  plugins,
  registry,
  useThemeName,
} from "@/lib/aigui";

type ResponseProps = ComponentProps<"div"> & {
  children: string;
};

/**
 * Response renders assistant output through AIGUI: markdown streams in
 * progressively, while the richer blocks a model emits — tables, math, code,
 * mermaid diagrams, ECharts charts, source lists, and any card registered in
 * lib/aigui — render as real UI once they are complete.
 *
 * AIRenderer is controlled by `text`: handing it the full string on every
 * update lets it push just the delta, which is exactly the shape useChat keeps
 * a streaming message in.
 *
 * The text is repaired on the way in — see normalizeOneLineBlocks. Every place
 * assistant output is shown goes through here, so the repair happens once.
 */
/**
 * A link in an answer must not navigate the window. A web link opens in the
 * browser; a path (what an agent writes for a file it made, e.g.
 * "outputs/report.pptx") opens that file from the workspace with its own app.
 */
function onLinkClick(e: MouseEvent<HTMLDivElement>) {
  const a = (e.target as HTMLElement).closest("a");
  if (!a) return;
  const href = a.getAttribute("href") ?? "";
  if (!href || href.startsWith("#")) return;
  e.preventDefault();
  if (isExternallyOpenable(href)) {
    openExternal(href);
    return;
  }
  let path = href.replace(/^file:\/\//, "").replace(/^\.\//, "");
  try {
    path = decodeURIComponent(path);
  } catch {}
  if (/^[a-z][a-z0-9+.-]*:/i.test(path)) return; // some other scheme: leave it
  OpenWorkspaceFileExternal(path)
    .then((res) => {
      if (res !== "ok") toast.error(res);
    })
    .catch((err: any) => toast.error(String(err?.message ?? err)));
}

export const Response = memo(
  ({ className, children, ...props }: ResponseProps) => {
    const theme = useThemeName();
    return (
      <div
        className={cn(
          "ai-response prose-sm max-w-none [&>*:first-child]:mt-0 [&>*:last-child]:mb-0",
          className,
        )}
        onClickCapture={onLinkClick}
        {...props}
      >
        <AIRenderer
          className="md"
          text={normalizeOneLineBlocks(children)}
          registry={registry}
          plugins={plugins}
          nodeRenderers={nodeRenderers}
          theme={theme}
          locale={APP_LOCALE}
        />
      </div>
    );
  },
  (prev, next) => prev.children === next.children,
);

Response.displayName = "Response";
