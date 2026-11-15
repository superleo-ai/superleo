import { useEffect, useState } from "react";
import { CheckIcon, CopyIcon, GlobeIcon } from "lucide-react";
import { PhoneLinks } from "../../wailsjs/go/app/App";
import { app } from "../../wailsjs/go/models";
import { EventsOn } from "../../wailsjs/runtime/runtime";
import { toast } from "../lib/toasts";
import { translate } from "../lib/i18n";
import { Chips, Group, IconBtn, Row, Seg, State, Text } from "./settings/kit";

type Mode = "" | "off" | "custom";

const MODES: { value: Mode; label: string }[] = [
  { value: "", label: "SuperLeo relay" },
  { value: "custom", label: "My own relay" },
  { value: "off", label: "Local network only" },
];

async function copy(text: string) {
  try {
    await navigator.clipboard.writeText(text);
  } catch {
    const rt = (window as any).runtime;
    if (rt?.ClipboardSetText) await rt.ClipboardSetText(text);
  }
}

/**
 * How a paired phone reaches this desktop when it is not on the same network
 * (app/phonelink.go): through our relay, a relay of the person's own, or not
 * at all, plus addresses of their own tunnels. Edits the page's copy of the
 * settings (phone_relay, phone_urls); the page's Save applies them, and the
 * backend restarts the phone link with them. What each choice means is on
 * the site (docs/site/reach.html), not here.
 */
export default function PhoneReachCard({ relay, urls, saved, onChange }: {
  relay: string;
  urls: string[];
  /** phone_relay and phone_urls as last saved: the link is asked again when they change. */
  saved?: unknown;
  onChange: (p: { phone_relay?: string; phone_urls?: string[] }) => void;
}) {
  const [info, setInfo] = useState<app.PhoneLinkInfo | null>(null);
  const [custom, setCustom] = useState(false);
  const [copied, setCopied] = useState(false);
  const off = relay.trim().toLowerCase() === "off";
  const mode: Mode = off ? "off" : relay.trim() !== "" || custom ? "custom" : "";

  useEffect(() => {
    let live = true;
    const load = () => PhoneLinks().then((i) => live && setInfo(i)).catch(() => live && setInfo(null));
    void load();
    // The link restarts after a save, and the relay connects in its own time.
    const t = window.setInterval(load, 5000);
    const stop = EventsOn("phonelink:changed", load);
    return () => {
      live = false;
      window.clearInterval(t);
      stop?.();
    };
  }, [JSON.stringify(saved ?? null)]);

  const pick = (m: Mode) => {
    setCustom(m === "custom");
    // My own relay keeps what is typed; it is saved once there is a host.
    const next = m === "off" ? "off" : m === "custom" && !off ? relay : "";
    if (next !== relay) onChange({ phone_relay: next });
  };
  const add = (v: string) => {
    const next = v.split(/[\s,，]+/).map((x) => x.trim().replace(/\/+$/, "")).filter(Boolean);
    const bad = next.find((x) => !/^https:\/\/[^/?#\s]+$/i.test(x));
    if (bad) {
      toast.error(translate("{url} is not an https:// address", { url: bad }));
      return;
    }
    const fresh = next.filter((x) => !urls.some((u) => u.toLowerCase() === x.toLowerCase()));
    if (fresh.length) onChange({ phone_urls: [...urls, ...fresh] });
  };

  const env = info?.relay_env || "";
  const address = info?.relay || "";
  const status = !address
    ? <State tone="off">{mode === "off" ? "Local network only" : "Not running"}</State>
    : !info?.running
      ? <State tone="off">No phone paired</State>
      : info.relayed
        ? <State tone="ok">Linked</State>
        : <State tone="busy">Connecting…</State>;

  return (
    <Group icon={GlobeIcon} title="Away from home" testId="phone-reach">
      <Row label="Relay address" testId="phone-relay-address"
        sub={address ? <span className="st-note mono" style={{ overflowWrap: "anywhere" }} data-testid="phone-relay-url">{address}</span> : undefined}>
        {status}
        {address && (
          <IconBtn icon={copied ? CheckIcon : CopyIcon} label={copied ? "Copied" : "Copy"}
            onClick={async () => { await copy(address); setCopied(true); window.setTimeout(() => setCopied(false), 1400); }} />
        )}
      </Row>
      <Row label="Relay" sub={env ? <span className="mono">SUPERLEO_RELAY={env}</span> : undefined}>
        <Seg label="Relay" value={mode} options={MODES} onChange={pick} disabled={!!env} testId="phone-relay-mode" />
      </Row>
      {mode === "custom" && (
        <Row label="Relay host">
          <Text mono label="Relay host" value={off ? "" : relay} placeholder="relay.example.com:43443" disabled={!!env}
            onChange={(v) => { setCustom(true); onChange({ phone_relay: v }); }} testId="phone-relay-host" />
        </Row>
      )}
      <Row label="Own addresses" stack>
        <Chips mono testId="phone-urls" items={urls} removeLabel="Remove" addLabel="Add an address" placeholder="https://desk.example.com"
          onRemove={(u) => onChange({ phone_urls: urls.filter((x) => x !== u) })} onAdd={add} />
      </Row>
    </Group>
  );
}
