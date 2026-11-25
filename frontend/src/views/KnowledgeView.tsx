import { translate, useI18n } from "../lib/i18n";
import React, { useCallback, useEffect, useRef, useState } from "react";
import { graphSrc } from "../lib/hivelink";
import { graphLook } from "../lib/graphframe";
import { ClipboardSetText } from "../../wailsjs/runtime";
import { GraphView as startGraphView } from "../../wailsjs/go/app/App";
import { useDaylight } from "../lib/useDaylight";
import { Skeleton, SkeletonLines, SkeletonRows, SkeletonCards } from "../components/Skeleton";

/**
 * One page for what SuperLeo knows.
 *
 * Recall and the graph used to be two entries in the sidebar, and they read the
 * same CortexDB — one asks it by name, the other shows the whole thing. Kept
 * apart, using them together meant going back and forth: spot a node worth
 * understanding and leave to search for it; find a memory and leave to see what
 * it connects to. They are two views of one question, so they are one page.
 *
 * Searching also drives the graph. The 3D view already has a Find box; typing
 * here reaches into it, so a recall lights up the nodes it is about instead of
 * leaving the reader to spot them.
 */

/** What the backend says about the running view. See App.GraphView. */
interface GraphStatus {
  url: string;
  source: string;
  backend: string;
  nodes: number;
  edges: number;
  activity: boolean;
  error: string;
}

function normalize(raw: Record<string, any> | null): GraphStatus {
  return {
    url: String(raw?.url ?? ""),
    source: String(raw?.source ?? ""),
    backend: String(raw?.backend ?? "local"),
    nodes: Number(raw?.nodes ?? 0),
    edges: Number(raw?.edges ?? 0),
    activity: Boolean(raw?.activity),
    error: String(raw?.error ?? ""),
  };
}

/**
 * Where to point the frame.
 *
 * CortexDB binds the live view to 127.0.0.1 of the machine SuperLeo runs on, and
 * there is deliberately no option to widen that — the page is the whole brain
 * with no authentication in front of it.
 *
 * That address is only reachable from the browser when the browser is on that
 * machine, which inside the desktop window it always is. Over HTTP it usually
 * is not: a tab open at a domain would resolve 127.0.0.1 to the viewer's own
 * laptop, where nothing is listening.
 *
 * This used to be handled by not drawing a frame at all and explaining the
 * situation, which is honest and is not the feature working — and served over
 * a domain is how SuperLeo is actually used. So SuperLeo proxies the view
 * instead: it runs on that host, it can reach loopback, and it has the front
 * door the view has none of. See backend/graphproxy.go.
 */
const SERVED = Boolean((window as unknown as Record<string, unknown>).superleoServed);
// Same origin as this page, so the session cookie rides along and the proxy's
// authentication applies to the frame exactly as it does to everything else.



export default function KnowledgeView() {
  const { t } = useI18n();
  const [status, setStatus] = useState<GraphStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [copied, setCopied] = useState(false);
  const frameRef = useRef<HTMLIFrameElement | null>(null);
  const day = useDaylight();


  const load = useCallback(async () => {
    setLoading(true);
    try {
      setStatus(normalize(await startGraphView()));
    } catch (e: any) {
      setStatus(normalize({ error: String(e?.message || e) }));
    } finally {
      setLoading(false);
    }
  }, []);

  // The view starts here, on the first render of this page — not at app boot.
  useEffect(() => {
    load();
  }, [load]);

  // An empty brain and a brain that has not been read yet look identical, so
  // keep asking until something shows up. Once there are nodes the page's own
  // event stream keeps it current and this stops.
  useEffect(() => {
    if (!status || status.error || status.nodes > 0) return;
    const t = setInterval(load, 5000);
    return () => clearInterval(t);
  }, [status, load]);

  const copy = (url: string) => {
    ClipboardSetText(url).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 1200);
    });
  };

  const brain =
    status?.backend === "shared" ? "Shared brain" : "Local brain";

  const header = (
    <div className="view-header">
      <div className="view-title">{translate("Knowledge")}</div>
    </div>
  );

  let body: React.ReactNode;
  if (loading && !status) {
    body = (
      <div className="panel-scroll"><SkeletonCards n={3} h={120} /></div>
    );
  } else if (status?.error) {
    body = (
      <div className="panel-scroll">
        <div className="panel-grid">
          <div className="card">
            <div className="card-title">{brain} unavailable</div>
            <div className="report-error">⚠ {status.error}</div>
            <div className="card-desc" style={{ marginTop: 12 }}>
              {status.backend === "shared"
                ? "The shared brain is configured under Settings → Memory. Check the endpoint is right and the CortexDB server is running and new enough to answer graph_list_all."
                : "The local brain lives in this machine's SuperLeo data directory. If it will not open, another process may be holding it."}
            </div>
            <div style={{ marginTop: 14 }}>
              <button className="btn" onClick={load} disabled={loading}>
                {loading ? (
                  <>
                    <span className="spinner" /> Retrying…
                  </>
                ) : (
                  "Try again"
                )}
              </button>
            </div>
          </div>
        </div>
      </div>
    );
  } else {
    body = (
      <div className="graph-stage">
        {status?.nodes === 0 && (
          <div className="graph-note">
            {brain} has no entities yet, so the scene is empty. It fills in on its own as SuperLeo
            saves memories and draws relations.
          </div>
        )}
        {/* The viewer is CortexDB's own page in a frame, and its overlay of
            filters and view controls is laid out for a desktop: at 342px it
            squeezes the graph into a strip and breaks its own labels one word
            per line. Nothing here can restyle another app's document, and
            shipping a squeezed frame is worse than saying so. On a phone the
            frame is left out and the button below — which was always there —
            is the way in. */}
        <div className="graph-narrow">
          A 3D graph wants a wider screen than this. Open it in a browser, or
          come back on a desktop.
        </div>
        <iframe
          key={`${graphSrc() ?? status?.url}#${day}`}
          ref={frameRef}
          className="graph-frame"
          src={(graphSrc() ?? status?.url) ? graphLook((graphSrc() ?? status?.url)!, day) : undefined}
          title={t("CortexDB knowledge graph")}
        />
      </div>
    );
  }

  return (
    <div className="view kn-view">
      {header}
      {body}
    </div>
  );
}
