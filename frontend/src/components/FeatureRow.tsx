import { useState } from "react";
import { Feature, FeatureMap, setFeature, useFeatures } from "../lib/features";
import { Row, Toggle } from "./settings/kit";

/** One switch for a feature with a plain on and off (coding, wellbeing, finance). */
export default function FeatureRow({ feature, label, onChanged }: {
  feature: Exclude<Feature, "browser" | "desktop">;
  label: string;
  /** The features now off and this one's state, for the page's own copy of the settings. */
  onChanged?: (off: string[], on: boolean) => void;
}) {
  const f = useFeatures();
  const [busy, setBusy] = useState(false);
  const [err, setErr] = useState("");
  const pick = async (on: boolean) => {
    setBusy(true);
    setErr("");
    try {
      await setFeature(feature, on);
      const now = { ...(f ?? {}), [feature]: on } as FeatureMap;
      onChanged?.((["wellbeing", "finance"] as const).filter((k) => now[k] === false), on);
    } catch (e) {
      setErr(String(e instanceof Error ? e.message : e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Row label={label} testId={`feature-row-${feature}`} sub={err ? <span className="st-note bad">{err}</span> : undefined}>
      <Toggle label={label} on={!!f?.[feature]} disabled={busy || !f} onChange={(v) => void pick(v)} testId={`feature-${feature}`} />
    </Row>
  );
}
